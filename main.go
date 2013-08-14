package main

import (
	"errors"
	"flag"
	"fmt"
	"go/parser"
	"go/printer"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var curgodir, imp, dest string
var deps []string

var (
	copy    = flag.Bool("copy", true, "copy the code")
	rewrite = flag.Bool("rewrite", true, "rewrite include paths")
	commit  = flag.Bool("commit", true, "commit vendored package")
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [FLAGS] <package>\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	var err error

	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}
	imp = flag.Arg(0)
	dest = filepath.Base(imp)

	pkgdir := which(imp)
	if pkgdir == "" {
		log.Fatal("could not find package")
	}

	curgodir, err = lookupDir()
	if err != nil {
		log.Fatal(err)
	}

	depFile, err := ioutil.ReadFile("dependencies")
	if err == nil {
		deps = strings.Split(strings.TrimSpace(string(depFile)), "\n")
	}
	deps = append(deps, imp)

	var revision string
	if *copy {
		err = os.RemoveAll(imp)
		if err != nil {
			log.Fatal(err)
		}

		err = os.MkdirAll(dest, 0770)
		if err != nil {
			log.Fatal(err)
		}

		err = run("cp", "-r", pkgdir+"/", dest)
		if err != nil {
			log.Fatal(err)
		}

		revision = rev()

		scmdirs := []string{"/.git", "/.hg", "/.bzr"}
		for _, scmdir := range scmdirs {
			err = os.RemoveAll(dest + scmdir)
			if err != nil {
				log.Fatal(err)
			}
		}
	}

	if *rewrite {
		err = filepath.Walk(".", mangle)
		if err != nil {
			log.Fatal(err)
		}

		err = run("go", "fmt", "./...")
		if err != nil {
			log.Fatal(err)
		}
	}

	if *commit {
		run("git", "add", dest)
		run("git", "commit", "-m", fmt.Sprintf("Vendor %s revision %s", imp, revision), "--", dest)
	}
}

func which(pkg string) string {
	for _, top := range strings.Split(os.Getenv("GOPATH"), ":") {
		dir := top + "/src/" + pkg
		_, err := os.Stat(dir)
		if err == nil {
			return dir
		}
		p := err.(*os.PathError)
		if !os.IsNotExist(p.Err) {
			log.Print(err)
		}
	}
	return ""
}

func rev() string {
	if _, err := os.Stat(dest + "/.git"); err == nil {
		return revCmd("git", "rev-parse", "--verify", "HEAD")[:8]
	}
	if _, err := os.Stat(dest + "/.hg"); err == nil {
		return revCmd("hg", "id", "-i")[:8]
	}
	if _, err := os.Stat(dest + "/.bzr"); err == nil {
		return revCmd("bzr", "revno")
	}
	return ""
}

func revCmd(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Dir = dest
	res, err := cmd.Output()
	if err != nil {
		log.Fatal(err)
	}
	return strings.TrimSpace(string(res))
}

func lookupDir() (string, error) {
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		return "", errors.New("missing GOPATH")
	}

	dot, err := os.Getwd()
	if err != nil {
		return "", err
	}

	items := strings.Split(gopath, ":")
	for _, top := range items {
		top = top + "/src/"
		if strings.HasPrefix(dot, top) {
			return dot[len(top):], nil
		}
	}

	return "", errors.New("cwd not found in GOPATH")
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func mangle(path string, info os.FileInfo, err error) error {
	if err != nil {
		log.Print(err)
	}

	if !info.IsDir() && strings.HasSuffix(path, ".go") {
		err = mangleFile(path)
		if err != nil {
			log.Print(err)
		}
	}
	return nil
}

func mangleFile(path string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return err
	}

	var changed bool
	for _, s := range f.Imports {
		path, err := strconv.Unquote(s.Path.Value)
		if err != nil {
			return err // can't happen
		}
		for _, dep := range deps {
			if strings.HasPrefix(path, dep) {
				s.Path.Value = strconv.Quote(curgodir + "/" + filepath.Base(dep) + path[len(dep):])
				changed = true
			}
		}
	}

	if !changed {
		return nil
	}

	wpath := path + ".temp"
	w, err := os.Create(wpath)
	if err != nil {
		return err
	}

	err = printer.Fprint(w, fset, f)
	if err != nil {
		return err
	}

	err = w.Close()
	if err != nil {
		return err
	}

	return os.Rename(wpath, path)
}
