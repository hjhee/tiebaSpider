package main

import (
	"html/template"
	"log"
	"math/rand"
	"time"
)

const (
	numFetcher   = 50
	numParser    = 100
	numGenerator = 1
)

var outputTemplate *template.Template

func init() {
	rand.Seed(time.Now().UnixNano())
	outputTemplate = template.Must(template.ParseFiles("template/template1.html"))
}

func main() {
	done := make(chan struct{})
	defer close(done)

	pc, errcFetch := fetchHTMLList(done, "url.txt")
	_, errcParse := parseHTML(done, pc)
	// outputc, errcGenerate := generateHTML(done, outputTemplate, tempc)

	for {
		select {
		case err, ok := <-errcFetch:
			if !ok {
				errcFetch = nil
			} else {
				log.Fatalf("[Fetch] %v\n", err)
			}
		case err, ok := <-errcParse:
			if !ok {
				errcParse = nil
			} else {
				log.Fatalf("[Parse] %v\n", err)
			}
			// case err, ok := <-errcGenerate:
			// 	if !ok {
			// 		errcGenerate = nil
			// 	} else {
			// 		log.Fatalf("[Template] %v\n", err)
			// 	}
			// case file := <-outputc:
			// 	log.Printf("[Template] %s done\n", file)
			// }
			// if errcFetch == nil && errcParse == nil && errcGenerate == nil {
			// 	done = true
			// 	log.Printf("Job done!\n")
			// }
		}
		if errcFetch == nil && errcParse == nil {
			log.Printf("Job done!\n")
			break
		}
	}

	// if err := fetchURLFromList("url.txt"); err != nil {
	// 	log.Fatalf("error fetching url from list: %v", err)
	// }
	// ch, _ := parseHTMLFromFile("example/content.html")

	// f, err := os.OpenFile("example/example.html", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	// if err != nil {
	// 	log.Fatal("error creating output file: %v", err)
	// }
	// defer f.Close()
	// generateHTML(f, outputTemplate, ch)
}
