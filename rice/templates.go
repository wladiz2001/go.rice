package main

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"text/template"

	"github.com/nkovacs/streamquote"
	"github.com/valyala/fasttemplate"
)

var (
	tmplEmbeddedBox          *template.Template
	tagEscaper, tagUnescaper *strings.Replacer
)

// template with no compression
const templatePlainStr = `package {{.Package}}

import (
	"time"

	"github.com/GeertJohan/go.rice/embedded"
)

{{range .Boxes}}
func init() {

	// define files
	{{range .Files}}{{.Identifier}} := &embedded.EmbeddedFile{
		Filename:    {{.FileName | tagescape | printf "%q"}},
		FileModTime: time.Unix({{.ModTime}}, 0),

		Content:     string({{.Path | injectfile | printf "%q"}}),
	}
	{{end}}

	// define dirs
	{{range .Dirs}}{{.Identifier}} := &embedded.EmbeddedDir{
		Filename:    {{.FileName | tagescape | printf "%q"}},
		DirModTime: time.Unix({{.ModTime}}, 0),
		ChildFiles:  []*embedded.EmbeddedFile{
			{{range .ChildFiles}}{{.Identifier}}, // {{.FileName | tagescape | printf "%q"}}
			{{end}}
		},
	}
	{{end}}

	// link ChildDirs
	{{range .Dirs}}{{.Identifier}}.ChildDirs = []*embedded.EmbeddedDir{
		{{range .ChildDirs}}{{.Identifier}}, // {{.FileName | tagescape | printf "%q"}}
		{{end}}
	}
	{{end}}

	// register embeddedBox
	embedded.RegisterEmbeddedBox(` + "`" + `{{.BoxName}}` + "`" + `, &embedded.EmbeddedBox{
		Name: ` + "`" + `{{.BoxName}}` + "`" + `,
		Time: time.Unix({{.UnixNow}}, 0),
		Dirs: map[string]*embedded.EmbeddedDir{
			{{range .Dirs}}{{.FileName | tagescape | printf "%q"}}: {{.Identifier}},
			{{end}}
		},
		Files: map[string]*embedded.EmbeddedFile{
			{{range .Files}}{{.FileName | tagescape | printf "%q"}}: {{.Identifier}},
			{{end}}
		},
	})
}
{{end}}`

// template with compression
//it uses ascii85 for encode the bytes into string
const templateZipStr = `package {{.Package}}

import (
	"time"
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"io"

	"github.com/GeertJohan/go.rice/embedded"
)

func Decompress(str string) string {

	var in bytes.Buffer
	in.WriteString(str)
	dec := ascii85.NewDecoder(&in)

	zdecom, err := zlib.NewReader(dec)
	if err != nil {
		panic(err)
	}

	var buff bytes.Buffer
	io.Copy(&buff, zdecom)
	zdecom.Close()
	return buff.String()
}

{{range .Boxes}}
func init() {

	// define files
	{{range .Files}}{{.Identifier}} := &embedded.EmbeddedFile{
		Filename:    {{.FileName | tagescape | printf "%q"}},
		FileModTime: time.Unix({{.ModTime}}, 0),

		Content:    Decompress( string({{.Path | injectfile | printf "%q"}}) ),
	}
	{{end}}

	// define dirs
	{{range .Dirs}}{{.Identifier}} := &embedded.EmbeddedDir{
		Filename:    {{.FileName | tagescape | printf "%q"}},
		DirModTime: time.Unix({{.ModTime}}, 0),
		ChildFiles:  []*embedded.EmbeddedFile{
			{{range .ChildFiles}}{{.Identifier}}, // {{.FileName | tagescape | printf "%q"}}
			{{end}}
		},
	}
	{{end}}

	// link ChildDirs
	{{range .Dirs}}{{.Identifier}}.ChildDirs = []*embedded.EmbeddedDir{
		{{range .ChildDirs}}{{.Identifier}}, // {{.FileName | tagescape | printf "%q"}}
		{{end}}
	}
	{{end}}

	// register embeddedBox
	embedded.RegisterEmbeddedBox(` + "`" + `{{.BoxName}}` + "`" + `, &embedded.EmbeddedBox{
		Name: ` + "`" + `{{.BoxName}}` + "`" + `,
		Time: time.Unix({{.UnixNow}}, 0),
		Dirs: map[string]*embedded.EmbeddedDir{
			{{range .Dirs}}{{.FileName | tagescape | printf "%q"}}: {{.Identifier}},
			{{end}}
		},
		Files: map[string]*embedded.EmbeddedFile{
			{{range .Files}}{{.FileName | tagescape | printf "%q"}}: {{.Identifier}},
			{{end}}
		},
	})
}
{{end}}`

const (
	unescapeTag = "unescape:"
	injectTag   = "injectfile:"
)

// We can't use init function anymore beacuse of /c flag, then use this
func initTemplate() {
	var err error
	if tmplEmbeddedBox != nil {
		fmt.Println("Ya se inicio")
		return
	}
	// $ is used as the escaping character,
	// because it has no special meaning in go strings,
	// so it won't be changed by strconv.Quote.
	replacements := []string{"$", "$$", "{%", "{$%", "%}", "%$}"}
	reverseReplacements := make([]string, len(replacements))
	l := len(reverseReplacements) - 1
	for i := range replacements {
		reverseReplacements[l-i] = replacements[i]
	}
	tagEscaper = strings.NewReplacer(replacements...)
	tagUnescaper = strings.NewReplacer(reverseReplacements...)

	var templateStr string
	if flags.Compress {
		templateStr = templateZipStr
	} else {
		templateStr = templatePlainStr
	}

	// parse embedded box template
	tmplEmbeddedBox, err = template.New("embeddedBox").Funcs(template.FuncMap{
		"tagescape": func(s string) string {
			return fmt.Sprintf("{%%%v%v%%}", unescapeTag, tagEscaper.Replace(s))
		},
		"injectfile": func(s string) string {
			return fmt.Sprintf("{%%%v%v%%}", injectTag, tagEscaper.Replace(s))
		},
	}).Parse(templateStr)
	if err != nil {
		fmt.Printf("error parsing embedded box template: %s\n", err)
		os.Exit(-1)
	}
}

// embeddedBoxFasttemplate will inject file contents and unescape {% and %}.
func embeddedBoxFasttemplate(w io.Writer, src string) error {
	var err error
	ft, err := fasttemplate.NewTemplate(src, "{%", "%}")
	if err != nil {
		return fmt.Errorf("error compiling fasttemplate: %s\n", err)
	}

	converter := streamquote.New()

	_, err = ft.ExecuteFunc(w, func(w io.Writer, tag string) (int, error) {
		if strings.HasPrefix(tag, unescapeTag) {
			tag = strings.TrimPrefix(tag, unescapeTag)
			return w.Write([]byte(tagUnescaper.Replace(tag)))
		}
		if !strings.HasPrefix(tag, injectTag) {
			return 0, fmt.Errorf("invalid fasttemplate tag: %v", tag)
		}
		tag = strings.TrimPrefix(tag, injectTag)

		fileName, err := strconv.Unquote("\"" + tag + "\"")
		if err != nil {
			return 0, fmt.Errorf("error unquoting filename %v: %v\n", tag, err)
		}
		f, err := os.Open(tagUnescaper.Replace(fileName))
		if err != nil {
			return 0, fmt.Errorf("error opening file %v: %v\n", fileName, err)
		}

		var n int

		//Here we select to compress the files or not
		if flags.Compress {
			n, err = CompressTemplate(f, w)
		} else {
			n, err = converter.Convert(f, w)
		}
		f.Close()
		if err != nil {
			return n, fmt.Errorf("error converting file %v: %v\n", fileName, err)
		}

		return n, nil
	})
	if err != nil {
		return fmt.Errorf("error executing fasttemplate: %s\n", err)
	}

	return nil
}

// Function to Compress the files in the template
// The encode is made with ascci85 it can be slower but save space
func CompressTemplate(in io.Reader, out io.Writer) (int, error) {
	var b bytes.Buffer

	content, err := ioutil.ReadAll(in)
	if err != nil {
		return 0, err
	}

	zcomp, err := zlib.NewWriterLevel(&b, zlib.BestCompression)
	if err != nil {
		return 0, err
	}
	zcomp.Write(content)
	zcomp.Close()

	buf85 := make([]byte, ascii85.MaxEncodedLen(len(b.Bytes())))
	ascii85.Encode(buf85, b.Bytes())

	//there was a problem with double quotation if anyone know a better way to handle this ... plz...
	var buff = []byte(strconv.Quote(string(buf85)))
	buff = buff[1 : len(buff)-1]

	out.Write(buff)
	return len(buff), nil
}

type embedFileDataType struct {
	Package string
	Boxes   []*boxDataType
}

type boxDataType struct {
	BoxName string
	UnixNow int64
	Files   []*fileDataType
	Dirs    map[string]*dirDataType
}

type fileDataType struct {
	Identifier string
	FileName   string
	Path       string
	ModTime    int64
}

type dirDataType struct {
	Identifier string
	FileName   string
	Content    []byte
	ModTime    int64
	ChildDirs  []*dirDataType
	ChildFiles []*fileDataType
}
