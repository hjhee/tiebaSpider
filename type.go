package main

import (
	"encoding/json"
	"html/template"
	"net/url"
	"regexp"
	"sync"
	"sync/atomic"
)

// PageChannel share HTML task between fetcher and parser
type PageChannel struct {
	// parser get HTML pages from rec
	rec <-chan *HTMLPage

	// fetcher get URL from send
	send chan<- *HTMLPage

	// number of URL to be fetched and parsed
	ref int64

	// flag, whether all URLs from list are added to fetcher
	init int64
}

// Add task number
func (p *PageChannel) Add(n int64) {
	if n <= 0 {
		return
	}
	atomic.AddInt64(&p.ref, n)
}

// Del task number
func (p *PageChannel) Del(n int64) {
	if n <= 0 {
		return
	}
	atomic.AddInt64(&p.ref, -n)
}

// Ref returns task number
func (p *PageChannel) Ref() int64 {
	return atomic.LoadInt64(&p.ref)
}

// Inited returns whether all URLs are read from url.txt
func (p *PageChannel) Inited() {
	atomic.StoreInt64(&p.init, 1)
}

// IsDone returns whether all HTML page are fetched
func (p *PageChannel) IsDone() bool {
	return atomic.LoadInt64(&p.ref) <= 0 && atomic.LoadInt64(&p.init) != 0
}

// HTMLType tells parser how to parse the HTMLPage
type HTMLType int

const (
	// HTMLWebHomepage is the first page of a Tieba post
	HTMLWebHomepage HTMLType = iota

	// HTMLWebPage is a page of a Tieba post
	HTMLWebPage

	// HTMLJSON is the Lzl totalComment in JSON format
	HTMLJSON

	// HTMLLzlHome is the Lzl Comment of a comment in page 2 in JSON format
	HTMLLzlHome

	// HTMLLzl is the Lzl Comment of a comment in JSON format
	HTMLLzl

	// HTMLLocal is a local HTML or JSON file
	HTMLLocal

	// HTMLWebWAPHomepage is the first page of a wap post
	HTMLWebWAPHomepage

	// HTMLWebWAPPage supports fetching wap posts
	HTMLWebWAPPage

	// HTMLExternalResource containes external resources (i.e. images)
	HTMLExternalResource
)

// HTMLPage is a job for fetcher and parser
type HTMLPage struct {
	// URL of the Page
	URL *url.URL

	// Content is the HTML code of the Page
	Content []byte

	// Type indicates different types of Tieba data
	Type HTMLType

	// Close http.Response when finished parsing
	// Response *http.Response

	// Path where downloaded external resources are saved
	Path string
	// ThreadID links external resources to corresponding TemplateField
	ThreadID uint64
}

// TiebaField parse "data-field" of each thread
type TiebaField struct {
	Author struct {
		UserID   uint64 `json:"user_id"`
		UserName string `json:"user_name"` // 用户名
		// Props string `json:"props"`
	} `json:"author"`
	Content struct {
		PostID uint64 `json:"post_id"`
		// IsAnonym bool `json:"is_anonym"`
		ForumID  uint64 `json:"forum_id"`
		ThreadID uint64 `json:"thread_id"`
		Content  string `json:"content"` // 正文内容
		PostNO   uint64 `json:"post_no"` // 楼数
		// Type string `json:"type"`
		// CommentNum uint16 `json:"comment_num"`
		// Props string `json:"props"`
		// PostIndex uint64 `json:"post_index"`
		// PbTpoint *uint64 `json:"pb_tpoint"`
	} `json:"content"`
}

// LzlField parse Lzl JSON data
type LzlField struct {
	ErrNO  int64                      `json:"errno"`
	ErrMsg string                     `json:"errmsg"`
	Data   map[string]json.RawMessage `json:"data"`
}

// LzlContent is a comment of Lzl from totalComment
type LzlContent struct {
	// 	ThreadID  uint64        `json:"thread_id,string"`
	// 	PostID    uint64        `json:"post_id,string"`
	// CommentID uint64        `json:"comment_id,string"`
	Index     int64
	UserName  string        `json:"username"`
	Content   template.HTML `json:"content"`
	Timestamp int64         `json:"now_time"`
	Time      string
}

// LzlComment indicates the relationship between a Tieba posts and the attached Lzl comment
type LzlComment struct {
	Num     uint64        `json:"comment_num"`
	ListNum uint64        `json:"comment_list_num"`
	Info    []*LzlContent `json:"comment_info"`
	// Info []json.RawMessage `json:"comment_info"`
}

// LzlPageComment indicates the total number of LzlComments in a single comment
type LzlPageComment struct {
	TotalNum  uint64 `json:"total_num"`
	TotalPage uint64 `json:"total_page"`
}

// OutputField render Tieba post in template
type OutputField struct {
	UserName template.HTML
	Content  template.HTML
	PostNO   uint64
	PostID   uint64
	Time     string
}

// LzlMap provides a thread safe map insert method
type LzlMap struct {
	Map  map[uint64]*LzlComment
	lock *sync.Mutex
}

// Append LzlComment to Map with synchronization
func (lzl *LzlMap) Append(k uint64, c *LzlContent) {
	lzl.lock.Lock()
	lzl.Map[k].Info = append(lzl.Map[k].Info, c)
	lzl.lock.Unlock()
}

// Insert LzlComment to Map with synchronization
func (lzl *LzlMap) Insert(k uint64, v *LzlComment) {
	lzl.lock.Lock()
	lzl.Map[k] = v
	lzl.lock.Unlock()
}

// IsExist returns true if key is already in Map
func (lzl *LzlMap) IsExist(k uint64) bool {
	lzl.lock.Lock()
	_, ok := lzl.Map[k]
	lzl.lock.Unlock()
	return ok
}

// ExternalResourceMap keeps records of fetched external resources
type ExternalResourceMap struct {
	Map  map[string]interface{}
	lock *sync.Mutex
}

func (erm *ExternalResourceMap) Get(k string) bool {
	erm.lock.Lock()
	defer erm.lock.Unlock()
	if _, ok := erm.Map[k]; !ok {
		return false
	}
	return true
}

func (erm *ExternalResourceMap) Set(k string) {
	erm.lock.Lock()
	defer erm.lock.Unlock()
	erm.Map[k] = nil
}

func (erm *ExternalResourceMap) Put(k string) bool {
	erm.lock.Lock()
	defer erm.lock.Unlock()
	ret := false
	if _, ok := erm.Map[k]; !ok {
		ret = true
	}
	erm.Map[k] = nil
	return ret
}

// TemplateField stores all necessary information to render a HTML page
type TemplateField struct {
	Title     string
	Url       string
	ThreadID  uint64
	Comments  []*OutputField
	pagesLeft int64
	Lzls      *LzlMap // Key is PostID
	lzlsLeft  int64
	resLeft   int64
	mutex     *sync.RWMutex
	send      bool
	rendered  int64
	resMap    *ExternalResourceMap
}

// NetTemplateField returns a initialized struct
func NewTemplateField(threadID uint64) *TemplateField {
	tf := &TemplateField{
		ThreadID: threadID,
		Comments: make([]*OutputField, 0, 30),
		Lzls: &LzlMap{
			Map:  make(map[uint64]*LzlComment),
			lock: &sync.Mutex{},
		},
		mutex: &sync.RWMutex{},
		resMap: &ExternalResourceMap{
			Map:  make(map[string]interface{}),
			lock: &sync.Mutex{},
		},
	}
	return tf
}

// Send parsed Tieba posts to render
// https://misfra.me/optimizing-concurrent-map-access-in-go/
func (t *TemplateField) Send(c chan *TemplateField) {
	t.mutex.RLock()
	if !t.send {
		t.mutex.RUnlock()
		t.mutex.Lock()
		if !t.send {
			c <- t
			t.send = true
		}
		t.mutex.Unlock()
	} else {
		t.mutex.RUnlock()
	}
}

// AddPage adds the number of Page to be parsed
func (t *TemplateField) AddPage(n int64) {
	atomic.AddInt64(&t.pagesLeft, n)
}

// AddLzl adds the number of Lzls to be parsed
func (t *TemplateField) AddLzl(n int64) {
	atomic.AddInt64(&t.lzlsLeft, n)
}

// Append a new post to TemplateField
func (t *TemplateField) Append(post *OutputField) {
	t.mutex.Lock()
	// l := len(t.Comments)
	// n := l + 1
	// if n > cap(t.Comments) {
	// 	newSlice := make([]*OutputField, 30*10+n+1)
	// 	copy(newSlice, t.Comments)
	// 	t.Comments = newSlice
	// }
	// t.Comments = t.Comments[0:n]
	// copy(t.Comments[n:n+1], post)
	t.Comments = append(t.Comments, post)
	t.mutex.Unlock()
}

// IsDone returns whether TemplateField is ready to be rendered
func (t *TemplateField) IsDone() bool {
	pagesLeft := atomic.LoadInt64(&t.pagesLeft)
	lzlsLeft := atomic.LoadInt64(&t.lzlsLeft)
	ret := pagesLeft <= 0 && lzlsLeft <= 0
	if config.StoreExternalResource {
		resLeft := atomic.LoadInt64(&t.resLeft)
		// log.Printf("%d: resLeft (%d)", t.ThreadID, resLeft)
		ret = ret && (resLeft <= 0)
	}
	return ret
}

// Merge consecutive posts whose Useaname is the same
func (t *TemplateField) Merge() {
	l := len(t.Comments)
	for i := 0; i+1 < l; i++ {
		if t.Comments[i+1].UserName != t.Comments[i].UserName {
			continue
		}
		v, ok := t.Lzls.Map[t.Comments[i+1].PostID]
		if ok && v.ListNum != 0 && v.Num != 0 {
			continue
		}
		v, ok = t.Lzls.Map[t.Comments[i].PostID]
		if ok && v.ListNum != 0 && v.Num != 0 {
			continue
		}
		// How to efficiently concatenate strings in Go?
		// https://stackoverflow.com/a/43675122/6091246
		bs := make([]byte, len(t.Comments[i].Content)+len(t.Comments[i+1].Content)+1)
		bl := 0
		bl += copy(bs[bl:], t.Comments[i].Content)
		bs[bl] = '\n'
		bl++
		bl += copy(bs[bl:], t.Comments[i+1].Content)
		t.Comments[i].Content = template.HTML(bs)
		// t.Comments[i].Content = t.Comments[i].Content + "\n" + t.Comments[i+1].Content
		// removes duplicate values in given slice
		// https://gist.github.com/alioygur/16c66b4249cb42715091fe010eec7e33#file-unique_slice-go-L13
		t.Comments = append(t.Comments[:i+1], t.Comments[i+2:]...)
		i--
		l--
	}
}

// Unique removes any duplicate posts using PoseNO
func (t *TemplateField) Unique() {
	// Idiomatic way to remove duplicates in a slice
	// https://www.reddit.com/r/golang/comments/5ia523/idiomatic_way_to_remove_duplicates_in_a_slice/db6qa2e/
	seen := make(map[uint64]struct{}, len(t.Comments))
	j := 0
	for _, v := range t.Comments {
		if _, ok := seen[v.PostNO]; ok {
			continue
		}
		seen[v.PostNO] = struct{}{}
		t.Comments[j] = v
		j++
	}
	t.Comments = t.Comments[:j]
}

// Rendered returns true if the template is written to the output file
func (t *TemplateField) Rendered() bool {
	return atomic.LoadInt64(&t.rendered) != 0
}

// SetRendered could be used to change rendered status
func (t *TemplateField) SetRendered(status bool) {
	if status {
		atomic.StoreInt64(&t.rendered, 1)
	} else {
		atomic.StoreInt64(&t.rendered, 0)
	}
}

func (t *TemplateField) FileName() string {
	// #6: remove illegal character in title
	// ref: https://www.codeproject.com/tips/758861/removing-characters-which-are-not-allowed-in-windo
	filenameRegex := regexp.MustCompile(`[\\/:*?""<>|]`)
	validFilename := filenameRegex.ReplaceAllLiteralString(t.Title, "")
	return validFilename
}

// TemplateMap manipulate a Tieba thread in parser
type TemplateMap struct {
	Map     map[uint64]*TemplateField // Key is ThreadID
	lock    *sync.RWMutex
	Channel chan *TemplateField
}

// Get returns a value from Map with synchronization
// see: https://misfra.me/optimizing-concurrent-map-access-in-go/ for more detail
func (tm *TemplateMap) Get(k uint64) *TemplateField {
	var val *TemplateField
	var ok bool
	tm.lock.RLock()
	if val, ok = tm.Map[k]; !ok {
		tm.lock.RUnlock()
		tm.lock.Lock()
		if val, ok = tm.Map[k]; !ok {
			val = NewTemplateField(k)
			tm.Map[k] = val
		}
		tm.lock.Unlock()
	} else {
		tm.lock.RUnlock()
	}
	return val
}

// Sweep search threadIDs for elements ready for rendering
func (tm *TemplateMap) Sweep(pc *PageChannel) {
	tm.lock.RLock()
	for k := range tm.Map {
		tf := tm.Map[k]
		if tf.IsDone() && !tf.Rendered() {
			go tf.Send(tm.Channel)
		}
	}
	tm.lock.RUnlock()

	// TODO: delete rendered threads from TemplateMap
}
