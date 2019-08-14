package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"math/rand"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
	threadRegex := regexp.MustCompile(`\b"?thread_id"?:"?(\d+)"?\b`)
	match := threadRegex.FindStringSubmatch(string(page.Content))
	strInt, _ := strconv.ParseInt(match[1], 10, 64)
	threadID := uint64(strInt)
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

		tf.pagesLeft = pageNum
		tf.lzlsLeft = pageNum
		// fetch all comments and lzls, excluding comments in the first page
		pc.Add(pageNum - 1 + pageNum)
		go func() {
			for i := int64(2); i <= pageNum; i++ {
				u := &url.URL{}
				*u = *page.URL
				q := u.Query()
				q.Set("pn", strconv.Itoa(int(i)))
				u.RawQuery = q.Encode()
				newPage := &HTMLPage{
					URL:  u, // example: https://tieba.baidu.com/p/3922635509?pn=2
					Type: HTMLWebPage,
				}
				select {
				case <-done:
					return
				case pc.send <- newPage: // add all other pages to fetcher
				}
			}

			forumRegex := regexp.MustCompile(`\b"?forum_id"?:"?(\d+)"?\b`)
			match := forumRegex.FindStringSubmatch(string(page.Content))
			strInt, _ := strconv.ParseInt(match[1], 10, 64)
			forumID := uint64(strInt)
			// fetch lzl comments
			// syntax:
			// http://tieba.baidu.com/p/totalComment?t=1501582373&tid=3922635509&fid=867983&pn=2&see_lz=0
			// python爬取贴吧楼中楼
			// https://mrxin.github.io/2015/09/19/tieba-louzhonglou/
			for i := int64(0); i <= pageNum; i++ {
				u := &url.URL{
					Scheme: "http",
					Host:   "tieba.baidu.com",
					Path:   "/p/totalComment",
				}
				q := u.Query()
				// Go by Example: Epoch
				// https://gobyexample.com/epoch
				q.Set("t", strconv.Itoa(int(time.Now().UnixNano()/1000000)))
				q.Set("tid", strconv.Itoa(int(tf.ThreadID)))
				q.Set("fid", strconv.Itoa(int(forumID)))
				q.Set("pn", strconv.Itoa(int(i)))
				u.RawQuery = q.Encode()
				log.Printf("requesting totalComment: %s", u)
				select {
				case <-done:
					return
				case pc.send <- &HTMLPage{
					URL:  u,
					Type: HTMLJSON,
				}:
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
	// log.Printf("[Parse] parsing %s", page.URL.String())
	err := htmlParse(pc, page, tmMap, func(tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) error {
		defer func() {
			if tf.IsDone() {
				tf.Send(tmMap.Channel) // avoid duplicate task
				// tmMap.Channel <- tf
			}
		}()
		defer tf.AddPage(-1)
		posts.Each(func(i int, s *goquery.Selection) {
			// filter elements that has more than 4 class (maybe an advertisement)
			classStr, _ := s.Attr("class") // get class string
			if len(strings.Fields(classStr)) > 4 {
				return
			}

			dataField, ok := s.Attr("data-field")
			if !ok {
				// maybe not an error, but an older version of data-field
				// log.Printf("#%d data-field not found: %s", i, page.URL.String()) // there's a error on the page, maybe Tieba updated the syntax
				return
			}

			var tiebaPost TiebaField
			var res OutputField
			err := json.Unmarshal([]byte(dataField), &tiebaPost)
			if err != nil {
				log.Printf("#%d data-field unmarshal failed: %v, url: %s", i, err, page.URL) // there's a error on the page, maybe Tieba updated the syntax
				return
			}
			res.UserName = tiebaPost.Author.UserName
			res.Content = template.HTML(tiebaPost.Content.Content)
			res.PostNO = tiebaPost.Content.PostNO
			res.PostID = tiebaPost.Content.PostID

			if res.Content == "" {
				// data-field does not contain content
				// infer an old version of posts
				postID := fmt.Sprintf("#post_content_%d", res.PostID)
				content, err := posts.Find(postID).Html()
				if err != nil {
					log.Printf("#%d: post_content_%d parse failed, %s", i, res.PostID, err)
				} else {
					res.Content = template.HTML(content)
				}
			}

			// get post time
			// Jquery过滤选择器，选择前几个元素，后几个元素，内容过滤选择器等
			// http://www.cnblogs.com/alone2015/p/4962687.html
			res.Time = s.Find("span.tail-info:nth-child(4)").Text() // posted from device other than PC
			if res.Time == "" {
				res.Time = s.Find("span.tail-info:nth-child(3)").Text() // posted from PC
			}

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

// parse lzl comment, JSON formatted
func jsonParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap, callback func(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap, tf *TemplateField) error) error {
	defer pc.Del(1)
	var threadID uint64

	u := page.URL
	q := u.Query()
	tid := q.Get("tid")
	if tid == "" {
		return fmt.Errorf("Error parsing getting tid from %s", page.URL.String()) // skip illegal URL
	}
	ret, _ := strconv.Atoi(tid)
	threadID = uint64(ret)

	var tf *TemplateField
	tf = tmMap.Get(threadID)
	defer func() {
		if tf.IsDone() {
			tf.Send(tmMap.Channel) // avoid duplicate task
			// tmMap.Channel <- tf
		}
	}()
	return callback(done, page, pc, tmMap, tf)
}

func requestLzlComment(tid string, pid string, pn string, tp HTMLType, pc *PageChannel) {
	// there are more lzls to fetch
	// url syntax:
	// url example: https://tieba.baidu.com/p/comment?tid=5381698176&pid=114248941589&pn=4&t=1517692202100
	u := &url.URL{
		Scheme: "http",
		Host:   "tieba.baidu.com",
		Path:   "/p/comment",
	}
	q := u.Query()
	q.Set("t", strconv.Itoa(int(time.Now().UnixNano()/1000000)))
	q.Set("tid", tid)
	q.Set("pid", pid)
	q.Set("pn", pn) // start fetching additional comment from page 2
	u.RawQuery = q.Encode()

	// log.Printf("requesting %s", u)

	pc.Add(1)
	go func() {
		pc.send <- &HTMLPage{
			URL:  u,
			Type: tp,
		}
	}()
}

func totalCommentParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	err := jsonParser(done, page, pc, tmMap, func(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap, tf *TemplateField) error {
		defer tf.AddLzl(-1)
		var lzl LzlField
		err := json.Unmarshal([]byte(string(page.Content)), &lzl)
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
		err = json.Unmarshal([]byte(string(commentList)), &comments)
		if err != nil {
			return fmt.Errorf("Error parsing comment_list from %s: %v\ncomment_list:\n%s", page.URL.String(), err, commentList)
		}

		if len(comments) == 0 {
			return nil // does not have any comments, stop
		}

		for pid, v := range comments {
			if tf.Lzls.IsExist(pid) {
				// totalComment contains lzls in different pages, which are duplicate
				continue
			}
			// normalize
			for i, comment := range v.Info {
				comment.Index = int64(i)
				comment.Time = time.Unix(comment.Timestamp, 0).In(time.Local).Format("2006-01-02 15:04")
			}
			// merge maps
			// Getting the union of two maps in go
			// https://stackoverflow.com/a/22621838/6091246
			numLeft := int64(v.Num) - int64(v.ListNum)
			if numLeft > 0 {
				// extend Lzl slice if needed
				if n := len(v.Info); uint64(n) < v.Num {
					// extend slice
					newSlice := make([]*LzlContent, n, v.Num+1)
					copy(newSlice, v.Info)
					v.Info = newSlice
				}
				tf.AddLzl(1)
				requestLzlComment(strconv.Itoa(int(tf.ThreadID)), strconv.Itoa(int(pid)), "2", HTMLLzlHome, pc)
			}
			tf.Lzls.Insert(pid, v) // merge maps
		}
		return nil
	})
	return err
}

func commentParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	err := jsonParser(done, page, pc, tmMap, func(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap, tf *TemplateField) error {
		defer tf.AddLzl(-1)
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page.Content))
		if err != nil {
			return fmt.Errorf("Error parsing %s: %v", page.URL, err)
		}
		q := page.URL.Query()
		tid := q.Get("tid")
		pid := q.Get("pid")
		pn := q.Get("pn")
		if page.Type == HTMLLzlHome {
			s := doc.Find("li.lzl_li_pager_s")
			dataField, ok := s.Attr("data-field")
			if !ok {
				return fmt.Errorf("Error parsing %s: total number of pages is not determinable", page.URL)
			}
			var lzlPage LzlPageComment
			err := json.Unmarshal([]byte(dataField), &lzlPage)
			if err != nil {
				return fmt.Errorf("LzlPageComment data-field unmarshal failed: %v, url: %s", err, page.URL)
			}
			tf.AddLzl(int64(lzlPage.TotalPage - 2))
			for i := uint64(3); i <= lzlPage.TotalPage; i++ {
				requestLzlComment(tid, pid, strconv.Itoa(int(i)), HTMLLzl, pc)
			}
		}
		exLzls := doc.Find(".lzl_single_post.j_lzl_s_p")
		exLzls.Each(func(i int, s *goquery.Selection) {
			pageNum, _ := strconv.Atoi(pn)
			key, _ := strconv.Atoi(pid)
			content, err := s.Find(".lzl_content_main").Html()
			if err != nil {
				return
			}
			user := s.Find(".j_user_card.lzl_p_p")
			userName, ok := user.Attr("username")
			if !ok {
				// userName not found
				log.Printf("ExLzl: cannot find username for pid=%s, index=%d", pid, i+pageNum*10)
				return
			}
			c := &LzlContent{
				Index:    int64(i + pageNum*10),
				UserName: userName,
				Content:  template.HTML(content),
				Time:     s.Find(".lzl_time").Text(),
			}
			tf.Lzls.Append(uint64(key), c)
		})
		return nil
	})
	return err
}

// parse templateField from local file, JSON formatted
func templateParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	defer pc.Del(1)
	var threadID uint64

	u := page.URL
	q := u.Query()
	tid := q.Get("tid")
	if tid == "" {
		return fmt.Errorf("Error parsing getting tid from %s", page.URL.String()) // skip illegal URL
	}
	ret, _ := strconv.Atoi(tid)
	threadID = uint64(ret)

	var tf *TemplateField
	tf = tmMap.Get(threadID)

	tf.mutex.Lock()
	err := json.Unmarshal([]byte(string(page.Content)), tf)
	tf.mutex.Unlock()
	if err != nil {
		return fmt.Errorf("Error parsing template file %s: %v", page.URL.String(), err)
	}
	tf.Send(tmMap.Channel)

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
				return // quit when pc.rec is closed
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
				err = totalCommentParser(done, p, pc, tmMap)
				if err != nil {
					errc <- err
				}
			case HTMLLzlHome, HTMLLzl:
				err = commentParser(done, p, pc, tmMap)
				if err != nil {
					errc <- err
				}
			case HTMLLocal:
				err = templateParser(done, p, pc, tmMap)
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
			log.Printf("[pc] jobs: %d", pc.Ref()) // status report
			if pc.IsDone() {
				close(pc.send) // no more task, tell fetcher to exit
				break
			}
			time.Sleep(time.Second) // check task number every second
		}
		wg.Wait() // wait parser finish all remaining tasks
		close(errc)
		close(tmMap.Channel) // all page parsed, tell renderer to exit
	}()
	return tmMap.Channel, errc
}
