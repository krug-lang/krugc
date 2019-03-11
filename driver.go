package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/krug-lang/krugc-api/api"
	"github.com/krug-lang/krugc-api/front"
	"github.com/krug-lang/krugc-api/ir"
)

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
const (
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
)

// https://medium.com/@kpbird/golang-generate-fixed-size-random-string-dd6dbd5e63c0
func randString(n int) string {
	b := make([]byte, n)
	for i := 0; i < n; {
		if idx := int(rand.Int63() & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i++
		}
	}
	return string(b)
}

func (cf *compilerFrontend) postRequest(route string, data interface{}) ([]byte, []api.CompilerError) {
	startTime := time.Now()

	// first we encode the data with gob
	mCache := new(bytes.Buffer)
	encCache := gob.NewEncoder(mCache)
	encCache.Encode(data)

	// then we pass it to the server in a json request.
	krugReq := api.KrugRequest{
		Data: mCache.Bytes(),
	}
	jsonData, err := json.Marshal(&krugReq)
	if err != nil {
		panic(err)
	}
	buff := bytes.NewBuffer(jsonData)

	reqPath := fmt.Sprintf("http://%s%s", cf.server, route)

	client := http.Client{}
	request, err := http.NewRequest("POST", reqPath, buff)
	if err != nil {
		panic(err)
	}

	resp, err := client.Do(request)
	if err != nil {
		panic(err)
	}

	var krugResp api.KrugResponse
	if err := json.NewDecoder(resp.Body).Decode(&krugResp); err != nil {
		panic(err)
	}

	elapsedTime := time.Now().Sub(startTime)
	fmt.Printf("> %-45s %v\n", reqPath, elapsedTime)
	return krugResp.Data, krugResp.Errors
}

type compilerFrontend struct {
	errors []api.CompilerError
	server string

	// TODO this should be a hashmap
	// and the errors should tell us what file
	// from the hashmap it's from
	sourceFiles map[string]front.KrugCompilationUnit
}

func (cf *compilerFrontend) writeError(err api.CompilerError) {
	fmt.Println(err.Title)

	// if we have some code points, extract the given code smaples
	codePoints := err.CodeContext
	for i := 0; i < len(codePoints); i += 2 {
		fst, snd := codePoints[i], codePoints[i+1]

		// this is a hack, fixme
		// there is only one file sothis loop will only
		// iterate once. when we add multiple files
		// we will want to be told in the CompilerError
		// what file this error ocurred in so we can look it up correctly.
		for _, file := range cf.sourceFiles {
			fmt.Println()
			fmt.Printf(" |>    %s\n", file.GetLine(fst, snd))
			fmt.Println()
		}
	}
}

// reportErrors will report all of the given errors. will return
// if the compiler can continue or not.
func (cf *compilerFrontend) reportErrors(errors []api.CompilerError) bool {
	hasFatal := false

	for _, err := range errors {
		if err.Fatal {
			hasFatal = true
		}

		cf.writeError(err)
	}

	cf.errors = append(cf.errors, errors...)
	return hasFatal
}

func main() {
	server := flag.String("server", "127.0.0.1:8001", "the krug-caas ip:port, e.g. 127.0.0.1:8001")
	codeGen := flag.Bool("gen", false, "when present, krug will generate code")
	dumpTokens := flag.Bool("dumptokens", false, "when present, the tokens will be dumped to stdout")

	flag.Parse()

	startTime := time.Now()

	cf := compilerFrontend{
		[]api.CompilerError{},
		*server,
		map[string]front.KrugCompilationUnit{},
	}

	filePaths := []string{}
	for _, arg := range os.Args[1:] {
		if strings.HasSuffix(arg, ".krug") {
			filePaths = append(filePaths, arg)
		}
	}

	trees := []front.ParseTree{}
	for _, filePath := range filePaths {
		compUnit := front.ReadCompUnit(filePath)
		cf.sourceFiles[filePath] = compUnit

		// LEXICAL ANALYSIS

		tokensRaw, errs := cf.postRequest("/front/lex", compUnit)
		if cf.reportErrors(errs) {
			return
		}

		var stream front.TokenStream
		tsDecoder := gob.NewDecoder(bytes.NewBuffer(tokensRaw))
		if err := tsDecoder.Decode(&stream); err != nil {
			panic(err)
		}

		if *dumpTokens {
			// dump tokens, debug for now.
			for _, tok := range stream.Tokens {
				fmt.Println(tok)
			}
		}

		// RECURSIVE DESCENT PARSE

		parseTreeRaw, errs := cf.postRequest("/front/parse", &stream)
		if cf.reportErrors(errs) {
			return
		}

		var resp front.ParseTree
		ptDecoder := gob.NewDecoder(bytes.NewBuffer(parseTreeRaw))
		if err := ptDecoder.Decode(&resp); err != nil {
			panic(err)
		}

		trees = append(trees, resp)
	}

	// BUILD IR

	irModuleRaw, errs := cf.postRequest("/ir/build", trees)
	if cf.reportErrors(errs) {
		return
	}

	var irMod ir.Module
	ptDecoder := gob.NewDecoder(bytes.NewBuffer(irModuleRaw))
	if err := ptDecoder.Decode(&irMod); err != nil {
		panic(err)
	}

	// SEMANTIC ANALYSIS OF IR

	// BUILD SCOPE

	scopedIrModuleRaw, errs := cf.postRequest("/mid/build_scope", irMod)
	if cf.reportErrors(errs) {
		return
	}

	var scopedIrMod ir.Module
	bsDecoder := gob.NewDecoder(bytes.NewBuffer(scopedIrModuleRaw))
	if err := bsDecoder.Decode(&scopedIrMod); err != nil {
		panic(err)
	}

	// PASSES.

	semaPassRoutes := []string{
		"type",
		"symbol",
	}

	for _, route := range semaPassRoutes {
		_, errs := cf.postRequest(fmt.Sprintf("/mid/resolve/%s", route), scopedIrMod)
		if cf.reportErrors(errs) {
			return
		}
	}

	// GENERATE CODE FOR IR.

	// if we have reported any errors, dont bother
	// trying to generate code.
	if len(cf.errors) == 0 && *codeGen {
		resp, errs := cf.postRequest("/back/gen", irMod)
		if cf.reportErrors(errs) {
			return
		}

		fmt.Println(string(resp))

		fileName := fmt.Sprintf("krug_main_%s.c", randString(16))
		if err := ioutil.WriteFile(fileName, resp, 0644); err != nil {
			panic(err)
		}
		defer func() {
			if err := os.Remove(fileName); err != nil {
				panic(err)
			}
		}()

		cmd := exec.Command("clang", fileName)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			panic(err)
		}
	}

	elapsed := time.Now().Sub(startTime)

	fmt.Printf("\n> %-45s %v\n", "total compilation time", elapsed)
}
