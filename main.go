package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/krug-lang/ir"
	"github.com/krug-lang/krugc-api/api"
	"github.com/krug-lang/krugc-api/front"
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

func postRequest(route string, data interface{}) ([]byte, error) {
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
		return nil, err
	}
	buff := bytes.NewBuffer(jsonData)

	client := http.Client{}
	request, err := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:8080%s", route), buff)
	if err != nil {
		log.Fatalln(err)
	}
	resp, err := client.Do(request)
	if err != nil {
		log.Fatalln(err)
	}

	var krugResp api.KrugResponse
	if err := json.NewDecoder(resp.Body).Decode(&krugResp); err != nil {
		panic(err)
	}
	return krugResp.Data, nil
}

func main() {
	startTime := time.Now()

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

		tokensRaw, err := postRequest("/front/lex", compUnit)
		if err != nil {
			panic(err)
		}

		var stream front.TokenStream
		tsDecoder := gob.NewDecoder(bytes.NewBuffer(tokensRaw))
		if tsDecoder.Decode(&stream); err != nil {
			panic(err)
		}

		// dump tokens, debug for now.
		for _, tok := range stream.Tokens {
			fmt.Println(tok)
		}

		// RECURSIVE DESCENT PARSE

		parseTreeRaw, err := postRequest("/front/parse", &stream)
		if err != nil {
			panic(err)
		}

		var resp front.ParseTree
		ptDecoder := gob.NewDecoder(bytes.NewBuffer(parseTreeRaw))
		if err := ptDecoder.Decode(&resp); err != nil {
			panic(err)
		}

		trees = append(trees, resp)
	}

	// BUILD IR

	irModuleRaw, err := postRequest("/ir/build", trees)
	if err != nil {
		panic(err)
	}

	var irMod ir.Module
	ptDecoder := gob.NewDecoder(bytes.NewBuffer(irModuleRaw))
	if err := ptDecoder.Decode(&irMod); err != nil {
		panic(err)
	}
	fmt.Println(irMod)

	// GENERATE CODE FOR IR.

	resp, err := postRequest("/back/gen", irMod)
	if err != nil {
		panic(err)
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
