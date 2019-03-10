package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
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

func postRequest(route string, data interface{}) ([]byte, []api.CompilerError) {
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

	client := http.Client{}
	request, err := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:8080%s", route), buff)
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
	return krugResp.Data, krugResp.Errors
}

type compilerFrontend struct {
	errors []api.CompilerError
}

// reportErrors will report all of the given errors. will return
// if the compiler can continue or not.
func (cf *compilerFrontend) reportErrors(errors []api.CompilerError) bool {
	hasFatal := false
	for _, err := range errors {
		if err.Fatal {
			hasFatal = true
		}
		fmt.Println(err.Title)
	}
	cf.errors = append(cf.errors, errors...)
	return hasFatal
}

func main() {
	startTime := time.Now()

	cf := compilerFrontend{
		[]api.CompilerError{},
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

		// LEXICAL ANALYSIS

		tokensRaw, errs := postRequest("/front/lex", compUnit)
		if cf.reportErrors(errs) {
			return
		}

		var stream front.TokenStream
		tsDecoder := gob.NewDecoder(bytes.NewBuffer(tokensRaw))
		if err := tsDecoder.Decode(&stream); err != nil {
			panic(err)
		}

		// dump tokens, debug for now.
		for _, tok := range stream.Tokens {
			fmt.Println(tok)
		}

		// RECURSIVE DESCENT PARSE

		parseTreeRaw, errs := postRequest("/front/parse", &stream)
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

	irModuleRaw, errs := postRequest("/ir/build", trees)
	if cf.reportErrors(errs) {
		return
	}

	var irMod ir.Module
	ptDecoder := gob.NewDecoder(bytes.NewBuffer(irModuleRaw))
	if err := ptDecoder.Decode(&irMod); err != nil {
		panic(err)
	}
	fmt.Println(irMod)

	// SEMANTIC ANALYSIS OF IR

	typeResolveRespRaw, errs := postRequest("/mid/type_resolve", irMod)
	if cf.reportErrors(errs) {
		return
	}

	fmt.Println(string(typeResolveRespRaw))

	// GENERATE CODE FOR IR.

	// if we have reported any errors, dont bother
	// trying to generate code.
	if len(cf.errors) != 0 {
		return
	}

	resp, errs := postRequest("/back/gen", irMod)
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

	elapsed := time.Now().Sub(startTime)
	fmt.Println("Compilation took", elapsed)
}
