package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"math/rand"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func htmlParse(pc *PageChannel, page *HTMLPage, tmMap *TemplateMap, callback func(tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) error) error {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page.Content))
	if err != nil {
		return fmt.Errorf("Error parsing %s: %v", page.URL, err)
	}
	posts := doc.Find("div.l_post.l_post_bright.j_l_post.clearfix")
	s := posts.First()
	dataField, ok := s.Attr("data-field")
	if !ok {
		return fmt.Errorf("first data-field not found")
	}
	var tiebaPost TiebaField
	err = json.Unmarshal([]byte(dataField), &tiebaPost)
	if err != nil {
		return fmt.Errorf("first data-field unmarshal failed: %v", err)
	}
	threadID := tiebaPost.Content.ThreadID
	forumID := tiebaPost.Content.ForumID
	var pageNum int
	q := page.URL.Query()
	if pn := q.Get("pn"); pn == "" {
		pageNum = 1
	} else {
		pageNum, err = strconv.Atoi(pn)
		if err != nil {
			pageNum = 1
		}
	}
	// http://tieba.baidu.com/p/totalComment?t=1501582373&tid=3922635509&fid=867983&pn=2&see_lz=0
	u := &url.URL{
		Scheme: "http",
		Host:   "tieba.baidu.com",
		Path:   "/p/totalComment",
	}
	q = u.Query()
	q.Set("t", strconv.Itoa(time.Now().Second()))
	q.Set("tid", strconv.Itoa(int(threadID)))
	q.Set("fid", strconv.Itoa(int(forumID)))
	q.Set("pn", strconv.Itoa(pageNum))
	u.RawQuery = q.Encode()

	pc.Add(1)
	go func() {
		pc.send <- &HTMLPage{
			URL:  u,
			Type: HTMLJSON,
		}
	}()

	tf := tmMap.Get(threadID)
	err = callback(tf, doc, posts)
	// page.Response.Body.Close()
	return err
}

func homepageParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	err := htmlParse(pc, page, tmMap, func(tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) error {
		var title string
		if s := doc.Find("title"); s.Text() == "" {
			title = randStringRunes(15) // Could not find title, default to random
		} else {
			title = s.Text()
		}
		log.Printf("[homepage] Title: %s", title)
		tf.Title = title

		var pageNum int64
		if s := doc.Find("span.red").Eq(1); s.Text() == "" {
			pageNum = 1 // Could not find total number of pages, default to 1
		} else {
			n, err := strconv.Atoi(s.Text())
			if err != nil {
				return fmt.Errorf("Error parsing total number of pages: %v", err)
			}
			pageNum = int64(n)
		}

		tf.PostsLeft = pageNum
		tf.LzlsLeft = pageNum
		pc.Add(pageNum - 2 + 1)
		go func() {
			for i := int64(2); i <= pageNum; i++ {
				u := &url.URL{}
				*u = *page.URL
				q := u.Query()
				q.Set("pn", fmt.Sprint(i))
				u.RawQuery = q.Encode()
				newPage := &HTMLPage{
					URL:  u,
					Type: HTMLWebPage,
				}
				select {
				case <-done:
					return
				case pc.send <- newPage:
				}
			}
		}()

		return nil
	})
	if err != nil {
		return err
	}
	return pageParser(done, page, pc, tmMap)
}

func pageParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	defer pc.Del(1)
	log.Printf("[Parse] parsing %s", page.URL.String())
	err := htmlParse(pc, page, tmMap, func(tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) error {
		defer func() {
			if tf.IsDone() {
				tf.Send(tmMap.Channel)
				// tmMap.Channel <- tf
			}
		}()
		defer tf.AddPosts(-1)
		posts.Each(func(i int, s *goquery.Selection) {
			dataField, ok := s.Attr("data-field")
			if !ok {
				log.Printf("#%d data-field not found: %s", i, page.URL.String()) // there's a error on the page, maybe Tieba updated the syntax
				return
			}
			var tiebaPost TiebaField
			var res OutputField
			err := json.Unmarshal([]byte(dataField), &tiebaPost)
			if err != nil {
				log.Printf("#%d data-field unmarshal failed: %v, url: %s", i, err, page.URL.String()) // there's a error on the page, maybe Tieba updated the syntax
				return
			}
			res.UserName = tiebaPost.Author.UserName
			res.Content = template.HTML(html.UnescapeString(tiebaPost.Content.Content))
			res.PostNO = tiebaPost.Content.PostNO
			res.PostID = tiebaPost.Content.PostID

			res.Time = s.Find("div.post-tail-wrap span.tail-info:nth-child(4)").Text() // get post time

			tf.Append(&res)
			// log.Printf("#%d data-field found: %v\n", i, tiebaPost)
			// log.Printf("#%d data-field found:\nauthor: %s\ncontent: %s\n",
			// 	tiebaPost.Content.PostNo,
			// 	tiebaPost.Author.UserName,
			// 	tiebaPost.Content.Content)

			// result.Posts <- &res
		})
		return nil
	})
	return err
}

func commentParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	defer pc.Del(1)
	var threadID uint64

	u := page.URL
	q := u.Query()
	if tid := q.Get("tid"); tid == "" {
		return fmt.Errorf("Error parsing getting tid from %s", page.URL.String())
	} else {
		ret, _ := strconv.Atoi(tid)
		threadID = uint64(ret)
	}

	var tf *TemplateField
	tf = tmMap.Get(threadID)
	defer func() {
		if tf.IsDone() {
			tf.Send(tmMap.Channel)
			// tmMap.Channel <- tf
		}
	}()
	defer tf.AddLzls(-1)

	var lzl LzlField
	err := json.Unmarshal(page.Content, &lzl)
	if err != nil {
		return fmt.Errorf("Error parsing content file %s: %v", page.URL.String(), err)
	}
	if lzl.ErrNO != 0 {
		return fmt.Errorf("Error getting data: %s, %s", page.URL.String(), lzl.ErrMsg)
	}
	commentList, ok := lzl.Data["comment_list"]
	if !ok {
		return fmt.Errorf("Error getting comment_list: %s", page.URL.String())
	}
	if string(commentList) == "" || string(commentList) == "[]" {
		return nil // comment list empty, stop
	}
	comments := make(map[uint64]*LzlComment)
	// var comments LzlComment
	err = json.Unmarshal(commentList, &comments)
	if err != nil {
		return fmt.Errorf("Error parsing comment_list from %s: %v\ncomment_list:\n%s", page.URL.String(), err, commentList)
	}

	if len(comments) == 0 {
		return nil // does not have any comments, stop
	}

	for k, v := range comments {
		tf.Lzls.Insert(k, v)
	}

	return nil
}

func parser(done <-chan struct{}, errc chan<- error, wg *sync.WaitGroup, pc *PageChannel, tmMap *TemplateMap) {
	defer wg.Done()
	var err error
	for {
		select {
		case <-done:
			return
		case p, ok := <-pc.rec:
			if !ok {
				return
			}
			switch p.Type {
			case HTMLWebHomepage:
				err = homepageParser(done, p, pc, tmMap)
				if err != nil {
					errc <- err
				}
			case HTMLWebPage:
				err = pageParser(done, p, pc, tmMap)
				if err != nil {
					errc <- err
				}
			case HTMLJSON:
				err = commentParser(done, p, pc, tmMap)
				if err != nil {
					errc <- err
				}
			default:
				errc <- errors.New("unkonwn HTMLPage Type")
			}
		}
	}
}

func parseHTML(done <-chan struct{}, pc *PageChannel) (<-chan *TemplateField, <-chan error) {
	tmMap := &TemplateMap{
		Map:     make(map[uint64]*TemplateField),
		lock:    &sync.RWMutex{},
		Channel: make(chan *TemplateField, numRenderer),
	}
	errc := make(chan error)

	var wg sync.WaitGroup
	wg.Add(numParser)
	for i := 0; i < numParser; i++ {
		go parser(done, errc, &wg, pc, tmMap)
	}
	go func() {
		for {
			log.Printf("[pc] jobs: %d", pc.Ref())
			if pc.IsDone() {
				close(pc.send)
				break
			}
			time.Sleep(time.Second)
		}
		wg.Wait()
		close(errc)
		close(tmMap.Channel)
	}()
	return tmMap.Channel, errc
}
