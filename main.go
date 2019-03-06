package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	jsoniter "github.com/json-iterator/go"
	"github.com/krug-lang/krugc-api/front"
)

func postRequest(route string, data front.KrugCompilationUnit, target interface{}) error {
	jsonData, err := jsoniter.Marshal(&data)
	if err != nil {
		return err
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
	return json.NewDecoder(resp.Body).Decode(target)
}

func main() {
	for _, file := range os.Args[1:] {
		fmt.Println("Compiling", file)
		compUnit := front.ReadCompUnit(file)

		var tokens front.TokenStream
		postRequest("/front/lex", compUnit, &tokens)
		fmt.Println("resp was", tokens)
	}
}
