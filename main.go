package main

import (
	"log"
	"os"
	"time"
	"math/rand"
	"html/template"
	// "log"
)

var outputTemplate *template.Template

func init() {
    rand.Seed(time.Now().UnixNano())
	outputTemplate = template.Must(template.ParseFiles("template/template1.html"))
	// if err := outputTemplate.Execute(os.Stdout, nil); err != nil {
	// 	log.Printf("error executing template: %v\n", err)
	// }
}

func main() {
	// if err := fetchURLFromList("url.txt"); err != nil {
	// 	log.Fatalf("error fetching url from list: %v", err)
	// }
	ch, _ := parseHTMLFromFile("example/content.html")

	f, err := os.OpenFile("example/example.html", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatal("error creating output file: %v", err)
	}
	defer f.Close()
	generateHTML(f, outputTemplate, ch)
}