package main

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/url"
	"path"
	"sync/atomic"

	"golang.org/x/net/html"
)

func postProcessing(done <-chan struct{}, pc *PageChannel, t *TemplateField) (bool, error) {
	select {
	case <-done:
		return false, nil
	default:
	}
	if config.HighResImage {
		var searchImageURL func(done <-chan struct{}, pc *PageChannel, t *TemplateField, root *html.Node) bool
		searchImageURL = func(done <-chan struct{}, pc *PageChannel, t *TemplateField, root *html.Node) bool {
			updated := false
			for c := root.FirstChild; c != nil; c = c.NextSibling {
				select {
				case <-done:
					return false
				default:
				}
				if c.Type == html.ElementNode && c.Data == "img" {
					for i, a := range c.Attr {
						if a.Key == "src" {
							// log.Printf("img: %s", a.Val)
							localSrc, ud := cvtHighResImageURL(done, pc, t, a.Val, t.FileName())
							updated = updated || ud
							c.Attr[i].Val = localSrc
						}
					}
				}
				ud := searchImageURL(done, pc, t, c)
				updated = updated || ud
			}
			return updated
		}
		if ret, err := processTemplateContent(done, pc, t, searchImageURL); err != nil {
			return false, err
		} else if ret {
			return true, nil
		}
	}
	if config.StoreExternalResource {
		var searchExternalResource func(done <-chan struct{}, pc *PageChannel, t *TemplateField, root *html.Node) bool
		searchExternalResource = func(done <-chan struct{}, pc *PageChannel, t *TemplateField, root *html.Node) bool {
			updated := false
			for c := root.FirstChild; c != nil; c = c.NextSibling {
				select {
				case <-done:
					return false
				default:
				}
				if c.Type == html.ElementNode && c.Data == "img" {
					for i, a := range c.Attr {
						if a.Key == "src" {
							localSrc, ud := cvtLocalURL(done, pc, t, a.Val, t.FileName())
							updated = updated || ud
							c.Attr[i].Val = localSrc
						}
					}
				}
				ud := searchExternalResource(done, pc, t, c)
				updated = updated || ud
			}
			return updated
		}

		if ret, err := processTemplateContent(done, pc, t, searchExternalResource); err != nil {
			return false, err
		} else if ret {
			return true, nil
		}
	}
	return false, nil
}

func processTemplateContent(done <-chan struct{}, pc *PageChannel, t *TemplateField, callback func(done <-chan struct{}, pc *PageChannel, t *TemplateField, root *html.Node) bool) (bool, error) {
	t.mutex.Lock()
	t.Lzls.lock.Lock()
	defer t.mutex.Unlock()
	defer t.Lzls.lock.Unlock()
	updated := false
	traverse := func(tpStr string, data interface{}) (template.HTML, bool, error) {
		executor := template.New("comment")
		executor.Parse(tpStr)
		var buf bytes.Buffer
		executor.Execute(&buf, data)
		node, err := html.Parse(&buf)
		if err != nil {
			return "", false, fmt.Errorf("failed to parse html node: %v", err)
		}
		buf.Reset()
		ud := callback(done, pc, t, node)
		if err := html.Render(&buf, node); err != nil {
			return "", false, fmt.Errorf("failed to render html template: %v", err)
		}
		return template.HTML(buf.String()), ud, nil
	}
	for k := range t.Comments {
		select {
		case <-done:
			return false, nil
		default:
		}
		var ud bool
		var err error
		// log.Printf("content (before): %s", t.Comments[k].Content)
		t.Comments[k].Content, ud, err = traverse(`{{.Content}}`, &t.Comments[k])
		updated = updated || ud
		// if err != nil {
		// 	return false, fmt.Errorf("[parseExternalResource] failed to parse comment (PostID: %s): %v", t.Comments[k].PostID, err)
		// }
		if err != nil {
			log.Printf("[parseExternalResource] failed to parse comment (ThreadID: %d, PostID: %d): %v", t.ThreadID, k, err)
		}
		// log.Printf("content (after): %s", t.Comments[k].Content)
	}
	for k := range t.Lzls.Map {
		select {
		case <-done:
			return false, nil
		default:
		}
		var ud bool
		var err error
		for i := range t.Lzls.Map[k].Info {
			// log.Printf("lzl content (before): %s", t.Lzls.Map[k].Info[i].Content)
			t.Lzls.Map[k].Info[i].Content, ud, err = traverse(`{{.Content}}`, &t.Lzls.Map[k].Info[i])
			// log.Printf("lzl content (after): %s", t.Lzls.Map[k].Info[i].Content)
			updated = updated || ud
			if err != nil {
				log.Printf("[parseExternalResource] failed to parse lzlComment (ThreadID: %d, PostID: %d, Index: %d): %v", t.ThreadID, k, t.Lzls.Map[k].Info[i].Index, err)
			}
		}
	}
	return updated, nil
}

func cvtLocalURL(done <-chan struct{}, pc *PageChannel, t *TemplateField, src, prefix string) (string, bool) {
	u, err := url.Parse(src)
	if err != nil {
		return src, false
	}
	uOrig := *u
	// url already converted
	if u.Scheme == "" {
		return src, false
	}
	u.Scheme = ""
	u.Path = fmt.Sprintf("%s%s", u.Host, u.Path)
	dst := fmt.Sprintf("res_%s/%s", prefix, u.Path)
	newSrc := dst
	if t.resMap.Put(newSrc) {
		atomic.AddInt64(&t.resLeft, 1)
		pc.Add(1)
		go func() {
			select {
			case pc.send <- &HTMLPage{URL: &uOrig, Type: HTMLExternalResource, Path: dst, ThreadID: t.ThreadID}:
			case <-done:
				return
			}
		}()
	}
	return newSrc, true
}

func cvtHighResImageURL(done <-chan struct{}, pc *PageChannel, t *TemplateField, src, prefix string) (string, bool) {
	u, err := url.Parse(src)
	if err != nil {
		return src, false
	}
	if u.Host != "c.hiphotos.baidu.com" {
		return src, false
	}
	imageName := path.Base(u.Path)
	u.Host = "imgsrc.baidu.com"
	u.Path = fmt.Sprintf("/forum/pic/item/%s", imageName)
	dst := u.String()
	newSrc := dst
	return newSrc, true
}
