# tiebaSpider

程序获取百度贴吧帖子的所有评论，包括所有楼中楼，以HTML和JSON为格式保存到本地，同时合并所有楼层连续、发帖人相同帖子方便阅读。

需要获取的帖子在`url.txt`中逐行指定。程序读取程序所在目录下的文件`url.txt`获取贴吧URL，逐行爬取URL指向的帖子。除了http协议的URL之外还支持file协议，file协议格式参考`url.txt`已有的URL。此功能主要用于验证程序功能或者调整HTML模板样式。所有已提取的帖子将命名为`file_{帖子主题}.{json,html}`保存至程序所在目录下的`output`文件夹。

## 特点

程序采用Go语言编写，利用goroutine同时获取、解析和渲染页面，各类goroutine的数量可以在`main.go`文件调整。

- 支持所有楼中楼评论
- 支持访问WAP版贴吧链接
- 图片链接替换为高清原图

## 模板

- 保存的HTML格式文件由`template/template1.html`的HTML模板定义。可以改写该文件以调整生成的HTML文件，从而美化界面或者嵌入Javascript脚本实现根据发帖人筛选帖子，比如只看楼主等自定义功能。模板的所有可指定的数据参考`type.go`的`TemplateField`定义，模板语法参考go官方文档。

- `template/template2.html`演示了如何利用模板文件通过javascript程序替换缩略图为高分辨率图片的链接。

## 待解决问题

以下问题需要解决，限于经验和时间问题无法进一步调查：

- 利用javascript程序检测图片加载失败的事件，并让浏览器自动重新加载
- 收集网页出现的所有图片并保存至本地，把所有图片内嵌至html
