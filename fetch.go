package main

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func fetchHTMLList(done <-chan struct{}, filename string) (*PageChannel, <-chan error) {
	feed := make(chan *HTMLPage, config.NumFetcher)
	ret, retErr := spawnFetcher(done, feed)

	pc := &PageChannel{send: feed, rec: ret}

	errc := make(chan error)
	go func() {
		defer close(errc)
		in, err := os.OpenFile(filename, os.O_RDONLY, 0644)
		if err != nil {
			errc <- fmt.Errorf("error reading url list: %v", err)
			return
		}
		defer in.Close()
		reader := bufio.NewReader(in)

		validURL := regexp.MustCompile(`^/p/([0-9]+)$`) // example: ^/p/7201761174$
		wapURL := regexp.MustCompile(`^/mo/m$`)         // example: "/mo/m?kz=7201761174"

		// reading file line by line in go
		// https://stackoverflow.com/a/41741702/6091246
		// case:
		// If you don't mind that the line could be very long (i.e. use a lot of RAM). It keeps the \n at the end of the string returned.
		var line string
		for isEOF := false; !isEOF; {
			line, err = reader.ReadString('\n')
			if err != nil {
				isEOF = true
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			u, err := url.Parse(strings.TrimSpace(line))
			if err != nil {
				log.Printf("[Fetch] Error parsing %s, skipping\n", line)
				continue
			}

			var pageType HTMLType

			if u.Scheme == "file" {
				pageType = HTMLLocal
				q := u.Query()
				tid := q.Get("tid") // get file tid for TemplateMap key later
				if tid == "" {
					log.Printf("[Fetch] file path %s is missing tid field, skipping", u)
					continue
				}
			} else {
				if u.Host != "tieba.baidu.com" {
					log.Printf("[Fetch] URL host %s is not Tieba, skipping", u)
					continue
				}

				if match := validURL.MatchString(u.Path); match {
					pageType = HTMLWebHomepage
					// strip query from url
					// URL Builder/Query builder in Go
					// https://stackoverflow.com/a/26987017/6091246
					u.RawQuery = ""
				} else if match = wapURL.MatchString(u.Path); match {
					pageType = HTMLWebWAPHomepage
				} else {
					log.Printf("[Fetch] %s is not a valid Tieba post URL, skipping", u)
					continue
				}
			}

			// log.Printf("[Fetch] Got new url from list: %v\n", u)

			pc.Add(1)
			select {
			case pc.send <- &HTMLPage{URL: u, Type: pageType}:
			case <-done:
				return
			}
		}
		pc.Inited()
	}()

	// merge error chans
	// Go Concurrency Patterns: Pipelines and cancellation
	// https://blog.golang.org/pipelines
	errChan := make(chan error)
	go func() {
		defer close(errChan)
		for {
			if errc == nil && retErr == nil {
				return
			}
			select {
			case err, ok := <-errc:
				if !ok {
					errc = nil
					continue
				}
				errChan <- err
				return

			case err, ok := <-retErr:
				if !ok {
					retErr = nil
					continue
				}
				errChan <- err
				return
			}
		}
	}()

	return pc, errChan
}

func fetcher(done <-chan struct{}, wg *sync.WaitGroup, jobsLeft *int64, ret chan<- *HTMLPage, jobs chan *HTMLPage) {
	defer wg.Done()
	for {
		select {
		case <-done:
			return
		case page, ok := <-jobs:
			if !ok {
				return
			}
			var err error
			switch page.Type {
			case HTMLLocal:
				err = fetchHTMLFromFile(page)
			default:
				err = fetchHTMLFromURL(page)
			}
			if err != nil {
				go func(page *HTMLPage) {
					select {
					case <-done:
						return
					case <-time.After(3 * time.Second):
						jobs <- page // add failed task back to jobs
					}
				}(page)
				log.Printf("[Fetch] error fetching %s, pause for 3s: %s\n", page.URL, err)
			} else {
				select {
				case ret <- page:
					atomic.AddInt64(jobsLeft, -1) // task done
				case <-done:
					return
				}
			}
		}
	}
}

func spawnFetcher(done <-chan struct{}, jobs <-chan *HTMLPage) (<-chan *HTMLPage, <-chan error) {
	in := make(chan *HTMLPage, config.NumFetcher) // fetcher get tasks from in
	ret := make(chan *HTMLPage, config.NumParser) // send HTML content to parser
	errc := make(chan error)

	jobsLeft := new(int64)
	chClosed := false

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(in)
		defer close(ret)
		for {
			if chClosed {
				if atomic.LoadInt64(jobsLeft) <= 0 {
					return // no more task left in channel in, exit
				}
				time.Sleep(time.Second) // check every second
				continue
			}
			select {
			case <-done:
				return
			case p, ok := <-jobs:
				if !ok {
					chClosed = true // parser sends no more jobs, time to exit
					continue
				}
				atomic.AddInt64(jobsLeft, 1) // add job to channel in
				in <- p
			}
		}
	}()
	for i := 0; i < config.NumFetcher; i++ {
		wg.Add(1)
		go fetcher(done, &wg, jobsLeft, ret, in)
	}
	go func() {
		wg.Wait()
		close(errc)
	}()
	return ret, errc
}

func fetchHTMLFromURL(page *HTMLPage) error {
	req, err := http.NewRequest("GET", page.URL.String(), nil)
	if err != nil {
		return err
	}
	if config.UserAgent != "" {
		req.Header.Add("User-Agent", config.UserAgent)
	}
	if config.CookieString != "" {
		req.Header.Add("Cookie", config.CookieString)
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	page.Content = bytes
	// page.Response = resp
	resp.Body.Close()
	return nil
}

func fetchHTMLFromFile(page *HTMLPage) error {
	in, err := os.OpenFile(page.URL.Path, os.O_RDONLY, 0644)
	if err != nil {
		return fmt.Errorf("error reading file path from %s: %v", page.URL.Path, err)
	}
	defer in.Close()
	reader := bufio.NewReader(in)
	bytes, err := ioutil.ReadAll(reader)
	if err != nil {
		return err
	}
	page.Content = bytes
	return nil
}
