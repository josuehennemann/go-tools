// Copyright (c) 2013 The Go Authors. All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file or at
// https://developers.google.com/open-source/licenses/bsd.

// Package lintutil provides helpers for writing linter command lines.
package lintutil // import "honnef.co/go/tools/lint/lintutil"

import (
	"errors"
	"flag"
	"fmt"
	"go/build"
	"go/types"
	"os"
	"sort"
	"strconv"
	"strings"

	"honnef.co/go/tools/lint"
	"honnef.co/go/tools/lint/lintutil/format"
	"honnef.co/go/tools/version"

	"golang.org/x/tools/go/packages"
)

func usage(name string, flags *flag.FlagSet) func() {
	return func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", name)
		fmt.Fprintf(os.Stderr, "\t%s [flags] # runs on package in current directory\n", name)
		fmt.Fprintf(os.Stderr, "\t%s [flags] packages\n", name)
		fmt.Fprintf(os.Stderr, "\t%s [flags] directory\n", name)
		fmt.Fprintf(os.Stderr, "\t%s [flags] files... # must be a single package\n", name)
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flags.PrintDefaults()
	}
}

type runner struct {
	checker       lint.Checker
	tags          []string
	ignores       []lint.Ignore
	version       int
	returnIgnored bool
}

func resolveRelative(importPaths []string, tags []string) (goFiles bool, err error) {
	if len(importPaths) == 0 {
		return false, nil
	}
	if strings.HasSuffix(importPaths[0], ".go") {
		// User is specifying a package in terms of .go files, don't resolve
		return true, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return false, err
	}
	ctx := build.Default
	ctx.BuildTags = tags
	for i, path := range importPaths {
		bpkg, err := ctx.Import(path, wd, build.FindOnly)
		if err != nil {
			return false, fmt.Errorf("can't load package %q: %v", path, err)
		}
		importPaths[i] = bpkg.ImportPath
	}
	return false, nil
}

func parseIgnore(s string) ([]lint.Ignore, error) {
	var out []lint.Ignore
	if len(s) == 0 {
		return nil, nil
	}
	for _, part := range strings.Fields(s) {
		p := strings.Split(part, ":")
		if len(p) != 2 {
			return nil, errors.New("malformed ignore string")
		}
		path := p[0]
		checks := strings.Split(p[1], ",")
		out = append(out, &lint.GlobIgnore{Pattern: path, Checks: checks})
	}
	return out, nil
}

type versionFlag int

func (v *versionFlag) String() string {
	return fmt.Sprintf("1.%d", *v)
}

func (v *versionFlag) Set(s string) error {
	if len(s) < 3 {
		return errors.New("invalid Go version")
	}
	if s[0] != '1' {
		return errors.New("invalid Go version")
	}
	if s[1] != '.' {
		return errors.New("invalid Go version")
	}
	i, err := strconv.Atoi(s[2:])
	*v = versionFlag(i)
	return err
}

func (v *versionFlag) Get() interface{} {
	return int(*v)
}

func FlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet("", flag.ExitOnError)
	flags.Usage = usage(name, flags)
	flags.String("tags", "", "List of `build tags`")
	flags.String("ignore", "", "Space separated list of checks to ignore, in the following format: 'import/path/file.go:Check1,Check2,...' Both the import path and file name sections support globbing, e.g. 'os/exec/*_test.go'")
	flags.Bool("tests", true, "Include tests")
	flags.Bool("version", false, "Print version and exit")
	flags.Bool("show-ignored", false, "Don't filter ignored problems")
	flags.String("f", "text", "Output `format` (valid choices are 'text' and 'json')")

	tags := build.Default.ReleaseTags
	v := tags[len(tags)-1][2:]
	version := new(versionFlag)
	if err := version.Set(v); err != nil {
		panic(fmt.Sprintf("internal error: %s", err))
	}

	flags.Var(version, "go", "Target Go `version` in the format '1.x'")
	return flags
}

type CheckerConfig struct {
	Checker     lint.Checker
	ExitNonZero bool
}

func ProcessFlagSet(confs map[string]CheckerConfig, fs *flag.FlagSet) {
	tags := fs.Lookup("tags").Value.(flag.Getter).Get().(string)
	ignore := fs.Lookup("ignore").Value.(flag.Getter).Get().(string)
	tests := fs.Lookup("tests").Value.(flag.Getter).Get().(bool)
	goVersion := fs.Lookup("go").Value.(flag.Getter).Get().(int)
	formatter := fs.Lookup("f").Value.(flag.Getter).Get().(string)
	printVersion := fs.Lookup("version").Value.(flag.Getter).Get().(bool)
	showIgnored := fs.Lookup("show-ignored").Value.(flag.Getter).Get().(bool)

	if printVersion {
		version.Print()
		os.Exit(0)
	}

	var cs []lint.Checker
	for _, conf := range confs {
		cs = append(cs, conf.Checker)
	}
	ps, err := Lint(cs, fs.Args(), &Options{
		Tags:          strings.Fields(tags),
		LintTests:     tests,
		Ignores:       ignore,
		GoVersion:     goVersion,
		ReturnIgnored: showIgnored,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var f format.Formatter
	switch formatter {
	case "text":
		f = format.Text{W: os.Stdout}
	case "stylish":
		f = &format.Stylish{W: os.Stdout}
	case "json":
		f = format.JSON{W: os.Stdout}
	default:
		fmt.Fprintf(os.Stderr, "unsupported output format %q\n", formatter)
		os.Exit(2)
	}

	var (
		total    int
		errors   int
		warnings int
	)

	total = len(ps)
	for _, p := range ps {
		conf, ok := confs[p.Checker]
		if !ok || conf.ExitNonZero {
			errors++
		} else {
			warnings++
		}
		f.Format(p)
	}
	if f, ok := f.(format.Statter); ok {
		f.Stats(total, errors, warnings)
	}
	if errors > 0 {
		os.Exit(1)
	}
}

type Options struct {
	Tags          []string
	LintTests     bool
	Ignores       string
	GoVersion     int
	ReturnIgnored bool
}

func Lint(cs []lint.Checker, paths []string, opt *Options) ([]lint.Problem, error) {
	if opt == nil {
		opt = &Options{}
	}
	ignores, err := parseIgnore(opt.Ignores)
	if err != nil {
		return nil, err
	}

	ctx := build.Default
	// XXX nothing cares about built tags right now
	ctx.BuildTags = opt.Tags
	conf := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Tests: opt.LintTests,
		TypeChecker: types.Config{
			// XXX this is not build system agnostic
			Sizes: types.SizesFor(ctx.Compiler, ctx.GOARCH),
		},
		Error: func(err error) {},
	}

	pkgs, err := packages.Load(conf, paths...)
	if err != nil {
		return nil, err
	}

	var problems []lint.Problem
	workingPkgs := make([]*packages.Package, 0, len(pkgs))
	for _, pkg := range pkgs {
		if pkg.IllTyped {
			problems = append(problems, compileErrors(pkg)...)
		} else {
			workingPkgs = append(workingPkgs, pkg)
		}
	}

	if len(workingPkgs) == 0 {
		return problems, nil
	}

	for _, c := range cs {
		runner := &runner{
			checker:       c,
			tags:          opt.Tags,
			ignores:       ignores,
			version:       opt.GoVersion,
			returnIgnored: opt.ReturnIgnored,
		}
		problems = append(problems, runner.lint(workingPkgs)...)
	}

	sort.Slice(problems, func(i int, j int) bool {
		pi, pj := problems[i].Position, problems[j].Position

		if pi.Filename != pj.Filename {
			return pi.Filename < pj.Filename
		}
		if pi.Line != pj.Line {
			return pi.Line < pj.Line
		}
		if pi.Column != pj.Column {
			return pi.Column < pj.Column
		}

		return problems[i].Text < problems[j].Text
	})

	if len(problems) < 2 {
		return problems, nil
	}

	uniq := make([]lint.Problem, 0, len(problems))
	uniq = append(uniq, problems[0])
	prev := problems[0]
	for _, p := range problems[1:] {
		if prev.Position == p.Position && prev.Text == p.Text {
			continue
		}
		prev = p
		uniq = append(uniq, p)
	}

	return uniq, nil
}

func compileErrors(pkg *packages.Package) []lint.Problem {
	if !pkg.IllTyped {
		return nil
	}
	if len(pkg.Errors) == 0 {
		// transitively ill-typed
		var ps []lint.Problem
		for _, imp := range pkg.Imports {
			ps = append(ps, compileErrors(imp)...)
		}
		return ps
	}
	var ps []lint.Problem
	for _, err := range pkg.Errors {
		var p lint.Problem
		switch err := err.(type) {
		case types.Error:
			p = lint.Problem{
				Position: err.Fset.Position(err.Pos),
				Text:     err.Msg,
				Checker:  "compiler",
				Check:    "compile",
			}
		default:
			fmt.Fprintf(os.Stderr, "internal error: unhandled error type %T\n", err)
		}
		ps = append(ps, p)
	}
	return ps
}

func ProcessArgs(name string, cs map[string]CheckerConfig, args []string) {
	flags := FlagSet(name)
	flags.Parse(args)

	ProcessFlagSet(cs, flags)
}

func (runner *runner) lint(initial []*packages.Package) []lint.Problem {
	l := &lint.Linter{
		Checker:       runner.checker,
		Ignores:       runner.ignores,
		GoVersion:     runner.version,
		ReturnIgnored: runner.returnIgnored,
	}
	return l.Lint(initial)
}
