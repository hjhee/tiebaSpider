package main

import (
	"net/http"
	"bufio"
	"log"
	"os"
	"io/ioutil"
)

func fetchURLFromList(filename string) error {
	in, err := os.OpenFile(filename, os.O_RDONLY, 0644)
	if err != nil {
		log.Printf("Error reading url list: %v\n", err)
		return err
	}
	reader := bufio.NewReader(in)

	var url string
    for isEOF:=false; !isEOF; {
        url, err = reader.ReadString('\n')
		if err != nil {
            isEOF = true
        }
		// Process the line here.
		// max URL length for IE is 2084: https://support.microsoft.com/zh-cn/help/208427/maximum-url-length-is-2-083-characters-in-internet-explorer
		if len(url) >= 2084 { 
			url = url[:2084]
		}

		log.Printf("Fetching %s:\n", url)
		resp, err := http.Get(url)
		if err != nil {
			log.Printf("Error fetching %s: %v", url, err)
			continue
		}

		bytes, _ := ioutil.ReadAll(resp.Body)
		resp.Body.Close()

		log.Println("HTML:\n\n", string(bytes)) // this is the html content
    }


	if err := in.Close(); err != nil {
		log.Printf("Error closing url list: %v\n", err)
		return err
	}

	return nil
}