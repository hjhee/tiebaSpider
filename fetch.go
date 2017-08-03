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

// http://tieba.baidu.com/p/totalComment?t=1501582373&tid=3922635509&fid=867983&pn=2&see_lz=0

func fetchHTMLList(done <-chan struct{}, filename string) (*PageChannel, <-chan error) {
	feed := make(chan *HTMLPage, numFetcher)
	ret, retErr := spawnFetcher(done, feed)

	pc := &PageChannel{send: feed, rec: ret}

	errc := make(chan error)
	go func() {
		defer close(errc)
		in, err := os.OpenFile(filename, os.O_RDONLY, 0644)
		if err != nil {
			errc <- fmt.Errorf("Error reading url list: %v", err)
			return
		}
		defer in.Close()
		reader := bufio.NewReader(in)

		validURL := regexp.MustCompile(`^/p/([0-9]+)$`)
		var wg sync.WaitGroup

		var line string
		for isEOF := false; !isEOF; {
			line, err = reader.ReadString('\n')
			if err != nil {
				isEOF = true
			}
			u, err := url.Parse(strings.TrimSpace(line))
			if err != nil {
				log.Printf("[Fetch] Error parsing %s, skipping\n", line)
				continue
			}

			if u.Host != "tieba.baidu.com" {
				log.Printf("[Fetch] %s is not from Tieba, skipping\n", u)
				continue
			}

			if match := validURL.Match([]byte(u.Path)); !match {
				log.Printf("[Fetch] %s is not a valid Tieba post URL, skipping\n", u)
				continue
			}

			u.RawQuery = ""

			log.Printf("[Fetch] Got new url from list: %v\n", u)

			wg.Add(1)
			go func(u *url.URL) {
				defer wg.Done()
				pc.Add(1)
				select {
				case pc.send <- &HTMLPage{URL: u, Type: HTMLWebHomepage}:
					return
				case <-done:
					return
				}
			}(u)
		}
		wg.Wait()
		pc.Inited()
	}()

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
			err := fetchHTMLFromURL(page)
			// err := fetchHTMLFromFile(page) // debug use
			if err != nil {
				go func(page *HTMLPage) {
					select {
					case <-done:
						return
					case <-time.After(3 * time.Second):
						jobs <- page
					}
				}(page)
				log.Printf("[Fetch] error fetching %s, pause for 3s: %s\n", page.URL, err)
			} else {
				select {
				case ret <- page:
					atomic.AddInt64(jobsLeft, -1)
				case <-done:
					return
				}
			}
		}
	}
}

func spawnFetcher(done <-chan struct{}, jobs <-chan *HTMLPage) (<-chan *HTMLPage, <-chan error) {
	in := make(chan *HTMLPage, numFetcher)
	ret := make(chan *HTMLPage, numParser)
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
					return
				}
				time.Sleep(time.Second)
				continue
			}
			select {
			case <-done:
				return
			case p, ok := <-jobs:
				if !ok {
					chClosed = true
					continue
				}
				atomic.AddInt64(jobsLeft, 1)
				in <- p
			}
		}
	}()
	for i := 0; i < numFetcher; i++ {
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
	resp, err := http.Get(page.URL.String())
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
	var filename string
	switch page.Type {
	case HTMLWebHomepage:
		filename = "example/content.html"
	case HTMLWebPage:
		filename = "example/content1.html"
	case HTMLJSON:
		filename = "example/lzl1.json"
	}
	in, err := os.OpenFile(filename, os.O_RDONLY, 0644)
	if err != nil {
		return fmt.Errorf("Error reading url list: %v", err)
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
