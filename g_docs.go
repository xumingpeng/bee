package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/astaxie/beego/swagger"
	"github.com/astaxie/beego/utils"
)

var globalDocsTemplate = `package docs

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/astaxie/beego"
	"github.com/astaxie/beego/swagger"
)

var rootinfo string = {{.rootinfo}}
var subapi string = {{.subapi}}
var rootapi swagger.ResourceListing

var apilist map[string]*swagger.ApiDeclaration

func init() {
	basepath := "/v1"
	err := json.Unmarshal([]byte(rootinfo), &rootapi)
	if err != nil {
		beego.Error(err)
	}
	err = json.Unmarshal([]byte(subapi), &apilist)
	if err != nil {
		beego.Error(err)
	}
	beego.Info(apilist)
	beego.GlobalDocApi["Root"] = rootapi
	for k, v := range apilist {
		for i, a := range v.Apis {
			a.Path = urlReplace(k + a.Path)
			v.Apis[i] = a
		}
		if beego.HttpAddr != "" {
			v.BasePath = beego.HttpAddr + ":" + strconv.Itoa(beego.HttpPort) + basepath
		} else {
			v.BasePath = "http://127.0.0.1:" + strconv.Itoa(beego.HttpPort) + basepath
		}
		beego.GlobalDocApi[strings.Trim(k, "/")] = v
	}
}

func urlReplace(src string) string {
	pt := strings.Split(src, "/")
	for i, p := range pt {
		if len(p) > 0 {
			if p[0] == ':' {
				pt[i] = "{" + p[1:] + "}"
			}
		}
	}
	return strings.Join(pt, "/")
}

`

var pkgCache map[string]bool //pkg:controller:function:comments comments: key:value
var controllerComments map[string]string
var importlist map[string]string
var apilist map[string]*swagger.ApiDeclaration
var controllerList map[string][]swagger.Api
var rootapi swagger.ResourceListing

func init() {
	cmdGenerate.Run = generateCode
	pkgCache = make(map[string]bool)
	controllerComments = make(map[string]string)
	importlist = make(map[string]string)
	apilist = make(map[string]*swagger.ApiDeclaration)
	controllerList = make(map[string][]swagger.Api)
}

func generateDocs(curpath string) {
	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, path.Join(curpath, "routers", "router.go"), nil, parser.ParseComments)

	if err != nil {
		ColorLog("[ERRO] parse router.go error\n")
		os.Exit(2)
	}

	rootapi.Infos = swagger.Infomation{}
	rootapi.SwaggerVersion = swagger.SwaggerVersion
	//analysis API comments
	if f.Comments != nil {
		for _, c := range f.Comments {
			for _, s := range strings.Split(c.Text(), "\n") {
				if strings.HasPrefix(s, "@APIVersion") {
					rootapi.ApiVersion = strings.TrimLeft(s, "@APIVersion ")
				} else if strings.HasPrefix(s, "@Title") {
					rootapi.Infos.Title = strings.TrimLeft(s, "@Title ")
				} else if strings.HasPrefix(s, "@Description") {
					rootapi.Infos.Description = strings.TrimLeft(s, "@Description ")
				} else if strings.HasPrefix(s, "@TermsOfServiceUrl") {
					rootapi.Infos.TermsOfServiceUrl = strings.TrimLeft(s, "@TermsOfServiceUrl ")
				} else if strings.HasPrefix(s, "@Contact") {
					rootapi.Infos.Contact = strings.TrimLeft(s, "@Contact ")
				} else if strings.HasPrefix(s, "@License") {
					rootapi.Infos.License = strings.TrimLeft(s, "@License ")
				} else if strings.HasPrefix(s, "@LicenseUrl") {
					rootapi.Infos.LicenseUrl = strings.TrimLeft(s, "@LicenseUrl ")
				}
			}
		}
	}
	for _, im := range f.Imports {
		analisyscontrollerPkg(im.Path.Value)
	}
	for _, d := range f.Decls {
		switch specDecl := d.(type) {
		case *ast.FuncDecl:
			for _, l := range specDecl.Body.List {
				switch smtp := l.(type) {
				case *ast.AssignStmt:
					for _, l := range smtp.Rhs {
						f, params := analisysNewNamespace(l.(*ast.CallExpr))
						for _, p := range params {
							switch pp := p.(type) {
							case *ast.CallExpr:
								if selname := pp.Fun.(*ast.SelectorExpr).Sel.String(); selname == "NSNamespace" {
									s, params := analisysNewNamespace(pp)
									subapi := swagger.ApiRef{Path: s}
									controllerName := ""
									for _, sp := range params {
										switch pp := sp.(type) {
										case *ast.CallExpr:
											if pp.Fun.(*ast.SelectorExpr).Sel.String() == "NSInclude" {
												controllerName = analisysNSInclude(s, pp)
											}
										}
									}
									if v, ok := controllerComments[controllerName]; ok {
										subapi.Description = v
									}
									rootapi.Apis = append(rootapi.Apis, subapi)
								} else if selname == "NSInclude" {
									analisysNSInclude(f, pp)
								}
							}
						}
					}
				}
			}
		}
	}
	apiinfo, err := json.Marshal(rootapi)
	if err != nil {
		panic(err)
	}
	subapi, err := json.Marshal(apilist)
	if err != nil {
		panic(err)
	}
	os.Mkdir(path.Join(curpath, "docs"), 0755)
	fd, err := os.Create(path.Join(curpath, "docs", "docs.go"))
	if err != nil {
		panic(err)
	}
	defer fd.Close()
	a := strings.Replace(globalDocsTemplate, "{{.rootinfo}}", "`"+string(apiinfo)+"`", -1)
	a = strings.Replace(a, "{{.subapi}}", "`"+string(subapi)+"`", -1)
	fd.WriteString(a)
}

func analisysNewNamespace(ce *ast.CallExpr) (first string, others []ast.Expr) {
	for i, p := range ce.Args {
		if i == 0 {
			switch pp := p.(type) {
			case *ast.BasicLit:
				first = strings.Trim(pp.Value, `"`)
			}
			continue
		}
		others = append(others, p)
	}
	return
}

func analisysNSInclude(baseurl string, ce *ast.CallExpr) string {
	cname := ""
	a := &swagger.ApiDeclaration{}
	a.ApiVersion = rootapi.ApiVersion
	a.SwaggerVersion = swagger.SwaggerVersion
	a.ResourcePath = baseurl
	a.Produces = []string{"application/json", "application/xml", "text/plain", "text/html"}
	a.Apis = make([]swagger.Api, 0)
	for _, p := range ce.Args {
		x := p.(*ast.UnaryExpr).X.(*ast.CompositeLit).Type.(*ast.SelectorExpr)
		if v, ok := importlist[fmt.Sprint(x.X)]; ok {
			cname = v + x.Sel.Name
		}
		if apis, ok := controllerList[cname]; ok {
			if len(a.Apis) > 0 {
				a.Apis = append(a.Apis, apis...)
			} else {
				a.Apis = apis
			}
		}
	}
	apilist[baseurl] = a
	return cname
}

func analisyscontrollerPkg(pkgpath string) {
	pkgpath = strings.Trim(pkgpath, "\"")
	pps := strings.Split(pkgpath, "/")
	importlist[pps[len(pps)-1]] = pkgpath
	if pkgpath == "github.com/astaxie/beego" {
		return
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		panic("please set gopath")
	}
	pkgRealpath := ""

	wgopath := filepath.SplitList(gopath)
	for _, wg := range wgopath {
		wg, _ = filepath.EvalSymlinks(filepath.Join(wg, "src", pkgpath))
		if utils.FileExists(wg) {
			pkgRealpath = wg
			break
		}
	}
	if pkgRealpath != "" {
		if _, ok := pkgCache[pkgpath]; ok {
			return
		}
	} else {
		ColorLog("[ERRO] the %s pkg not exist in gopath\n", pkgpath)
		os.Exit(1)
	}
	fileSet := token.NewFileSet()
	astPkgs, err := parser.ParseDir(fileSet, pkgRealpath, func(info os.FileInfo) bool {
		name := info.Name()
		return !info.IsDir() && !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".go")
	}, parser.ParseComments)

	if err != nil {
		ColorLog("[ERRO] the %s pkg parser.ParseDir error\n", pkgpath)
		os.Exit(1)
	}
	for _, pkg := range astPkgs {
		for _, fl := range pkg.Files {
			for _, d := range fl.Decls {
				switch specDecl := d.(type) {
				case *ast.FuncDecl:
					if specDecl.Recv != nil && len(specDecl.Recv.List) > 0 {
						if t, ok := specDecl.Recv.List[0].Type.(*ast.StarExpr); ok {
							parserComments(specDecl.Doc, specDecl.Name.String(), fmt.Sprint(t.X), pkgpath)
						}
					}
				case *ast.GenDecl:
					if specDecl.Tok.String() == "type" {
						for _, s := range specDecl.Specs {
							switch tp := s.(*ast.TypeSpec).Type.(type) {
							case *ast.StructType:
								_ = tp.Struct
								controllerComments[pkgpath+s.(*ast.TypeSpec).Name.String()] = specDecl.Doc.Text()
							}
						}
					}
				}
			}
		}
	}
}

// parse the func comments
func parserComments(comments *ast.CommentGroup, funcName, controllerName, pkgpath string) error {
	innerapi := swagger.Api{}
	opts := swagger.Operation{}
	if comments != nil && comments.List != nil {
		for _, c := range comments.List {
			t := strings.TrimSpace(strings.TrimLeft(c.Text, "//"))
			if strings.HasPrefix(t, "@router") {
				elements := strings.TrimLeft(t, "@router ")
				e1 := strings.SplitN(elements, " ", 2)
				if len(e1) < 1 {
					return errors.New("you should has router infomation")
				}
				innerapi.Path = e1[0]
				if len(e1) == 2 && e1[1] != "" {
					e1 = strings.SplitN(e1[1], " ", 2)
					opts.HttpMethod = strings.ToUpper(strings.Trim(e1[0], "[]"))
				} else {
					opts.HttpMethod = "GET"
				}
			} else if strings.HasPrefix(t, "@Title") {
				opts.Nickname = strings.TrimLeft(t, "@Title ")
			} else if strings.HasPrefix(t, "@Description") {
				opts.Summary = strings.TrimLeft(t, "@Description ")
			} else if strings.HasPrefix(t, "@Success") {
				rs := swagger.ResponseMessage{}
				st := strings.Split(strings.TrimLeft(t, "@Success "), " ")
				rs.Message = st[1]
				rs.ResponseModel = st[2]
				rs.Code, _ = strconv.Atoi(st[0])
				opts.ResponseMessages = append(opts.ResponseMessages, rs)
			} else if strings.HasPrefix(t, "@Param") {
				para := swagger.Parameter{}
				p := getparams(strings.TrimSpace(strings.TrimLeft(t, "@Param ")))
				para.Name = p[0]
				para.ParamType = p[1]
				para.DataType = p[2]
				if len(p) > 4 {
					para.Required, _ = strconv.ParseBool(p[3])
					para.Description = p[4]
				} else {
					para.Description = p[3]
				}
				opts.Parameters = append(opts.Parameters, para)
			} else if strings.HasPrefix(t, "@Failure") {
				rs := swagger.ResponseMessage{}
				st := strings.TrimLeft(t, "@Failure ")
				var cd []rune
				for i, s := range st {
					if s == ' ' {
						rs.Message = st[i+1:]
						break
					}
					cd = append(cd, s)
				}
				rs.Code, _ = strconv.Atoi(string(cd))
				opts.ResponseMessages = append(opts.ResponseMessages, rs)
			} else if strings.HasPrefix(t, "@Type") {
				opts.Type = strings.TrimLeft(t, "@Type ")
			}
		}
	}
	innerapi.Operations = append(innerapi.Operations, opts)
	if innerapi.Path != "" {
		if _, ok := controllerList[pkgpath+controllerName]; ok {
			controllerList[pkgpath+controllerName] = append(controllerList[pkgpath+controllerName], innerapi)
		} else {
			controllerList[pkgpath+controllerName] = make([]swagger.Api, 1)
			controllerList[pkgpath+controllerName][0] = innerapi
		}
	}
	return nil
}

// analisys params return []string
// @Param	query		form	 string	true		"The email for login"
// [query form string true "The email for login"]
func getparams(str string) []string {
	var s []byte
	var j int
	var start bool
	var r []string
	for i, c := range []byte(str) {
		if c == ' ' || c == '\t' || c == '\n' {
			if !start {
				continue
			} else {
				if j == 3 {
					r = append(r, string(s))
					r = append(r, strings.Trim((str[i+1:]), " \"\t\n"))
					break
				}
				start = false
				j++
				r = append(r, string(s))
				s = make([]byte, 0)
				continue
			}
		}
		start = true
		s = append(s, c)
	}
	return r
}
