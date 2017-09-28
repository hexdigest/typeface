package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/loader"

	"github.com/gojuno/generator"
)

type (
	options struct {
		InputFile      string
		OutputFile     string
		InterfaceName  string
		SourceTypeName string
		Package        string
	}

	methodInfo struct {
		Method *types.Signature
		Doc    *ast.CommentGroup
	}

	visitor struct {
		gen          *generator.Generator
		methods      map[string]methodInfo
		info         *loader.PackageInfo
		sourceStruct string
	}
)

func main() {
	opts := processFlags()
	packagePath := opts.InputFile

	if _, err := os.Stat(packagePath); err == nil {
		if packagePath, err = generator.PackageOf(packagePath); err != nil {
			die(err)
		}
	}

	destPackagePath, err := generator.PackageOf(filepath.Dir(opts.OutputFile))
	if err != nil {
		die(err)
	}

	cfg := loader.Config{
		AllowErrors:         true,
		ParserMode:          parser.ParseComments,
		TypeCheckFuncBodies: func(string) bool { return false },
		TypeChecker: types.Config{
			IgnoreFuncBodies:         true,
			FakeImportC:              true,
			DisableUnusedImportCheck: true,
			Error: func(err error) {},
		},
	}

	cfg.Import(packagePath)

	if err := os.Remove(opts.OutputFile); err != nil && !os.IsNotExist(err) {
		die(err)
	}

	if destPackagePath != packagePath {
		cfg.Import(destPackagePath)
	}

	prog, err := cfg.Load()
	if err != nil {
		die(err)
	}

	gen := generator.New(prog)
	gen.ImportWithAlias(destPackagePath, "")
	gen.SetPackageName(opts.Package)
	gen.SetVar("structName", opts.SourceTypeName)
	gen.SetVar("interfaceName", opts.InterfaceName)
	gen.SetVar("packagePath", packagePath)
	gen.SetHeader(fmt.Sprintf(`DO NOT EDIT!
This code was generated automatically using github.com/hexdigest/typeface
The original type %q can be found in %s package
You can generate mock for this interface using github.com/gojuno/minimock:

minimock -i %s.%s -o ./
`, opts.SourceTypeName, packagePath, destPackagePath, opts.InterfaceName))

	v := &visitor{
		gen:          gen,
		sourceStruct: opts.SourceTypeName,
		info:         prog.Package(packagePath),
		methods:      make(map[string]methodInfo),
	}

	pkg := prog.Package(packagePath)
	if pkg == nil {
		die(fmt.Errorf("unable to load package: %s", packagePath))
	}

	for _, file := range prog.Package(packagePath).Files {
		ast.Walk(v, file)
	}

	if len(v.methods) == 0 {
		die(fmt.Errorf("type %s was not found in %s or doesn't have any exported methods", opts.SourceTypeName, packagePath))
	}

	if err := gen.ProcessTemplate("", template, v.methods); err != nil {
		die(err)
	}

	if err := gen.WriteToFilename(opts.OutputFile); err != nil {
		die(err)
	}
}

// Visit implements ast.Visitor
func (v *visitor) Visit(node ast.Node) ast.Visitor {
	//we're only interested in public methods
	if ts, ok := node.(*ast.FuncDecl); ok && ts.Recv != nil && ts.Name.Name[0] == strings.ToUpper(ts.Name.Name)[0] {
		t, err := v.gen.ExpressionType(ts.Recv.List[0].Type)
		if err != nil {
			die(fmt.Errorf("failed to get expression for %T %s: %v", ts.Type, ts.Name.Name, err))
		}
		chunks := strings.Split(t.String(), ".")
		typeName := chunks[len(chunks)-1]

		if typeName == v.sourceStruct {
			if method, ok := v.info.ObjectOf(ts.Name).Type().(*types.Signature); ok {
				v.methods[ts.Name.Name] = methodInfo{
					Method: method,
					Doc:    ts.Doc,
				}
			}
		}

		return nil
	}

	return v
}

func (v *visitor) private() {}

const template = `
	//{{$interfaceName}} contains exportable methods signatures of the {{$packagePath}}.{{$structName}}
	type {{$interfaceName}} interface {
		{{ range $methodName, $methodInfo := . }}
		{{if $methodInfo.Doc }}{{range $i, $comment := $methodInfo.Doc.List}}{{$comment.Text}}
{{end}}{{end}}{{$methodName}}{{ signature $methodInfo.Method }}
		{{ end }}
	}`

func processFlags() *options {
	var (
		sname  = flag.String("s", "", "source struct type name")
		name   = flag.String("i", "", "name of the destination interface")
		input  = flag.String("f", "", "input file or import path of the package that contains struct type declaration")
		output = flag.String("o", "", "destination file name to place the generated interface")
		pkg    = flag.String("p", "", "destination package name")
	)

	flag.Parse()

	if *pkg == "" || *input == "" || *output == "" || *name == "" || *sname == "" || !strings.HasSuffix(*output, ".go") {
		flag.Usage()
		os.Exit(1)
	}

	return &options{
		InputFile:      *input,
		OutputFile:     *output,
		InterfaceName:  *name,
		Package:        *pkg,
		SourceTypeName: *sname,
	}
}

func die(err error) {
	fmt.Fprintf(os.Stderr, "%v\n", err)
	os.Exit(1)
}
