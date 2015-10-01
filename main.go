package main

import (
	"github.com/kr/tarutil"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	MaxTarSize = 2 * 1000 * 1000
)

var Q = make(chan *job)

var (
	version = capture("go", "version")
	distenv = capture("go", "tool", "dist", "env")
)

func main() {
	for i := 0; i < 5; i++ {
		go worker(i)
	}
	http.HandleFunc("/info", handleInfo)
	http.Handle("/build/", http.StripPrefix("/build/", http.HandlerFunc(handleBuild)))
	listen := ":" + os.Getenv("PORT")
	if listen == ":" {
		listen = ":8000"
	}
	err := http.ListenAndServe(listen, nil)
	if err != nil {
		panic(err)
	}
}

func handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Write(version)
	w.Write(distenv)
}

func handleBuild(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var insecure bool
	rawInsecure := r.FormValue("insecure")
	if rawInsecure != "" && rawInsecure == "true" {
		insecure = true
	}
	j := &job{
		insecure: insecure
		pkg:  r.URL.Path,
		tar:  http.MaxBytesReader(w, r.Body, MaxTarSize),
		done: make(chan struct{}),
	}
	Q <- j
	<-j.done
	const httpTooLarge = "http: request body too large"
	if j.err != nil && j.err.Error() == httpTooLarge {
		http.Error(w, httpTooLarge, http.StatusRequestEntityTooLarge)
		return
	}
	if j.err != nil {
		log.Println(j.err)
		http.Error(w, "unprocessable entity", 422)
		w.Write(j.out)
		return
	}
	defer j.bin.Close()
	http.ServeContent(w, r, "", time.Time{}, j.bin)
}

func worker(n int) {
	gopath := "/tmp/" + strconv.Itoa(n)
	for j := range Q {
		if err := build(j, gopath); err != nil {
			j.err = err
		}
		j.done <- struct{}{}
	}
}

func build(j *job, gopath string) error {
	defer os.RemoveAll(gopath)
	if err := os.RemoveAll(gopath); err != nil {
		return err
	}
	err := tarutil.ExtractAll(j.tar, gopath, 0)
	if err != nil {
		return err
	}
	cmdPkg := j.pkg
	if j.insecure {
		cmdPkg = " -insecure " + j.pkg
	}
	j.out, err = goget(gopath, cmdPkg)
	if err != nil {
		return err
	}
	j.bin, err = os.Open(gopath + "/bin/" + path.Base(j.pkg))
	return err
}

func goget(gopath, pkg string) ([]byte, error) {
	cmd := exec.Command("go", "get", pkg)
	for _, env := range os.Environ() {
		if strings.Contains(env,"GOPATH=") {
			env = "GOPATH="+gopath
		}
		cmd.Env = append(cmd.Env,env)
	}

	return cmd.CombinedOutput()
}

type job struct {
	tar    io.Reader
	pkg    string
	gopath string
	bin    *os.File
	out    []byte
	err    error
	insecure bool
	done   chan struct{}
}

func capture(name string, arg ...string) []byte {
	cmd := exec.Command(name, arg...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		panic(err)
	}
	return out
}
