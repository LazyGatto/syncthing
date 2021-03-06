// Copyright (C) 2014 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at http://mozilla.org/MPL/2.0/.

// +build ignore

package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	versionRe = regexp.MustCompile(`-[0-9]{1,3}-g[0-9a-f]{5,10}`)
	goarch    string
	goos      string
	noupgrade bool
	version   string
	race      bool
)

const minGoVersion = 1.3

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(0)

	if os.Getenv("GOPATH") == "" {
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatal(err)
		}
		gopath := filepath.Clean(filepath.Join(cwd, "../../../../"))
		log.Println("GOPATH is", gopath)
		os.Setenv("GOPATH", gopath)
	}
	os.Setenv("PATH", fmt.Sprintf("%s%cbin%c%s", os.Getenv("GOPATH"), os.PathSeparator, os.PathListSeparator, os.Getenv("PATH")))

	flag.StringVar(&goarch, "goarch", runtime.GOARCH, "GOARCH")
	flag.StringVar(&goos, "goos", runtime.GOOS, "GOOS")
	flag.BoolVar(&noupgrade, "no-upgrade", noupgrade, "Disable upgrade functionality")
	flag.StringVar(&version, "version", getVersion(), "Set compiled in version string")
	flag.BoolVar(&race, "race", race, "Use race detector")
	flag.Parse()

	switch goarch {
	case "386", "amd64", "arm":
		break
	default:
		log.Printf("Unknown goarch %q; proceed with caution!", goarch)
	}

	checkRequiredGoVersion()

	if flag.NArg() == 0 {
		var tags []string
		if noupgrade {
			tags = []string{"noupgrade"}
		}
		install("./cmd/...", tags)

		vet("./cmd/syncthing")
		vet("./internal/...")
		lint("./cmd/syncthing")
		lint("./internal/...")
		return
	}

	for _, cmd := range flag.Args() {
		switch cmd {
		case "setup":
			setup()

		case "install":
			pkg := "./cmd/..."
			var tags []string
			if noupgrade {
				tags = []string{"noupgrade"}
			}
			install(pkg, tags)

		case "build":
			pkg := "./cmd/syncthing"
			var tags []string
			if noupgrade {
				tags = []string{"noupgrade"}
			}
			build(pkg, tags)

		case "test":
			test("./...")

		case "assets":
			assets()

		case "xdr":
			xdr()

		case "translate":
			translate()

		case "transifex":
			transifex()

		case "deps":
			deps()

		case "tar":
			buildTar()

		case "zip":
			buildZip()

		case "deb":
			buildDeb()

		case "clean":
			clean()

		case "vet":
			vet("./cmd/syncthing")
			vet("./internal/...")

		case "lint":
			lint("./cmd/syncthing")
			lint("./internal/...")

		default:
			log.Fatalf("Unknown command %q", cmd)
		}
	}
}

func checkRequiredGoVersion() {
	ver := run("go", "version")
	re := regexp.MustCompile(`go version go(\d+\.\d+)`)
	if m := re.FindSubmatch(ver); len(m) == 2 {
		vs := string(m[1])
		// This is a standard go build. Verify that it's new enough.
		f, err := strconv.ParseFloat(vs, 64)
		if err != nil {
			log.Printf("*** Couldn't parse Go version out of %q.\n*** This isn't known to work, proceed on your own risk.", vs)
			return
		}
		if f < minGoVersion {
			log.Fatalf("*** Go version %.01f is less than required %.01f.\n*** This is known not to work, not proceeding.", f, minGoVersion)
		}
	} else {
		log.Printf("*** Unknown Go version %q.\n*** This isn't known to work, proceed on your own risk.", ver)
	}
}

func setup() {
	runPrint("go", "get", "-v", "golang.org/x/tools/cmd/cover")
	runPrint("go", "get", "-v", "golang.org/x/tools/cmd/vet")
	runPrint("go", "get", "-v", "golang.org/x/net/html")
	runPrint("go", "get", "-v", "github.com/tools/godep")
}

func test(pkg string) {
	setBuildEnv()
	runPrint("go", "test", "-short", "-timeout", "60s", pkg)
}

func install(pkg string, tags []string) {
	os.Setenv("GOBIN", "./bin")
	args := []string{"install", "-v", "-ldflags", ldflags()}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	if race {
		args = append(args, "-race")
	}
	args = append(args, pkg)
	setBuildEnv()
	runPrint("go", args...)
}

func build(pkg string, tags []string) {
	binary := "syncthing"
	if goos == "windows" {
		binary += ".exe"
	}

	rmr(binary, binary+".md5")
	args := []string{"build", "-ldflags", ldflags()}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	if race {
		args = append(args, "-race")
	}
	args = append(args, pkg)
	setBuildEnv()
	runPrint("go", args...)

	// Create an md5 checksum of the binary, to be included in the archive for
	// automatic upgrades.
	err := md5File(binary)
	if err != nil {
		log.Fatal(err)
	}
}

func buildTar() {
	name := archiveName()
	var tags []string
	if noupgrade {
		tags = []string{"noupgrade"}
		name += "-noupgrade"
	}
	build("./cmd/syncthing", tags)
	filename := name + ".tar.gz"
	files := []archiveFile{
		{src: "README.md", dst: name + "/README.txt"},
		{src: "LICENSE", dst: name + "/LICENSE.txt"},
		{src: "AUTHORS", dst: name + "/AUTHORS.txt"},
		{src: "syncthing", dst: name + "/syncthing"},
		{src: "syncthing.md5", dst: name + "/syncthing.md5"},
	}

	for _, file := range listFiles("etc") {
		files = append(files, archiveFile{src: file, dst: name + "/" + file})
	}
	for _, file := range listFiles("extra") {
		files = append(files, archiveFile{src: file, dst: name + "/" + filepath.Base(file)})
	}

	tarGz(filename, files)
	log.Println(filename)
}

func buildZip() {
	name := archiveName()
	var tags []string
	if noupgrade {
		tags = []string{"noupgrade"}
		name += "-noupgrade"
	}
	build("./cmd/syncthing", tags)
	filename := name + ".zip"
	files := []archiveFile{
		{src: "README.md", dst: name + "/README.txt"},
		{src: "LICENSE", dst: name + "/LICENSE.txt"},
		{src: "AUTHORS", dst: name + "/AUTHORS.txt"},
		{src: "syncthing.exe", dst: name + "/syncthing.exe"},
		{src: "syncthing.exe.md5", dst: name + "/syncthing.exe.md5"},
	}

	for _, file := range listFiles("extra") {
		files = append(files, archiveFile{src: file, dst: name + "/" + filepath.Base(file)})
	}

	zipFile(filename, files)
	log.Println(filename)
}

func buildDeb() {
	os.RemoveAll("deb")

	build("./cmd/syncthing", []string{"noupgrade"})

	files := []archiveFile{
		{src: "README.md", dst: "deb/usr/share/doc/syncthing/README.txt", perm: 0644},
		{src: "LICENSE", dst: "deb/usr/share/doc/syncthing/LICENSE.txt", perm: 0644},
		{src: "AUTHORS", dst: "deb/usr/share/doc/syncthing/AUTHORS.txt", perm: 0644},
		{src: "syncthing", dst: "deb/usr/bin/syncthing", perm: 0755},
	}

	for _, file := range listFiles("extra") {
		files = append(files, archiveFile{src: file, dst: "deb/usr/share/doc/syncthing/" + filepath.Base(file), perm: 0644})
	}

	for _, af := range files {
		if err := copyFile(af.src, af.dst, af.perm); err != nil {
			log.Fatal(err)
		}
	}

	debarch := goarch
	if debarch == "386" {
		debarch = "i386"
	}

	control := `Package: syncthing
Architecture: {{arch}}
Depends: libc6
Version: {{version}}
Maintainer: Syncthing Release Management <release@syncthing.net>
Description: Open Source Continuous File Synchronization
	Syncthing does bidirectional synchronization of files between two or
	more computers.
`
	changelog := `syncthing ({{version}}); urgency=medium

  * Packaging of {{version}}.

 -- Jakob Borg <jakob@nym.se>  {{date}}
`

	control = strings.Replace(control, "{{arch}}", debarch, -1)
	control = strings.Replace(control, "{{version}}", version[1:], -1)
	changelog = strings.Replace(changelog, "{{arch}}", debarch, -1)
	changelog = strings.Replace(changelog, "{{version}}", version[1:], -1)
	changelog = strings.Replace(changelog, "{{date}}", time.Now().Format(time.RFC1123), -1)

	os.MkdirAll("deb/DEBIAN", 0755)
	ioutil.WriteFile("deb/DEBIAN/control", []byte(control), 0644)
	ioutil.WriteFile("deb/DEBIAN/compat", []byte("9\n"), 0644)
	ioutil.WriteFile("deb/DEBIAN/changelog", []byte(changelog), 0644)

}

func copyFile(src, dst string, perm os.FileMode) error {
	dstDir := filepath.Dir(dst)
	os.MkdirAll(dstDir, 0755) // ignore error
	srcFd, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFd.Close()
	dstFd, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer dstFd.Close()
	_, err = io.Copy(dstFd, srcFd)
	return err
}

func listFiles(dir string) []string {
	var res []string
	filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			res = append(res, path)
		}
		return nil
	})
	return res
}

func setBuildEnv() {
	os.Setenv("GOOS", goos)
	os.Setenv("GOARCH", goarch)
	wd, err := os.Getwd()
	if err != nil {
		log.Println("Warning: can't determine current dir:", err)
		log.Println("Build might not work as expected")
	}
	os.Setenv("GOPATH", fmt.Sprintf("%s%c%s", filepath.Join(wd, "Godeps", "_workspace"), os.PathListSeparator, os.Getenv("GOPATH")))
	log.Println("GOPATH=" + os.Getenv("GOPATH"))
}

func assets() {
	setBuildEnv()
	runPipe("internal/auto/gui.files.go", "go", "run", "cmd/genassets/main.go", "gui")
}

func xdr() {
	runPrint("go", "generate", "./internal/discover", "./internal/db")
}

func translate() {
	os.Chdir("gui/assets/lang")
	runPipe("lang-en-new.json", "go", "run", "../../../cmd/translate/main.go", "lang-en.json", "../../index.html")
	os.Remove("lang-en.json")
	err := os.Rename("lang-en-new.json", "lang-en.json")
	if err != nil {
		log.Fatal(err)
	}
	os.Chdir("../../..")
}

func transifex() {
	os.Chdir("gui/assets/lang")
	runPrint("go", "run", "../../../cmd/transifexdl/main.go")
	os.Chdir("../../..")
	assets()
}

func deps() {
	rmr("Godeps")
	runPrint("godep", "save", "./cmd/...")
}

func clean() {
	rmr("bin", "Godeps/_workspace/pkg", "Godeps/_workspace/bin")
	rmr(filepath.Join(os.Getenv("GOPATH"), fmt.Sprintf("pkg/%s_%s/github.com/syncthing", goos, goarch)))
}

func ldflags() string {
	var b bytes.Buffer
	b.WriteString("-w")
	b.WriteString(fmt.Sprintf(" -X main.Version %s", version))
	b.WriteString(fmt.Sprintf(" -X main.BuildStamp %d", buildStamp()))
	b.WriteString(fmt.Sprintf(" -X main.BuildUser %s", buildUser()))
	b.WriteString(fmt.Sprintf(" -X main.BuildHost %s", buildHost()))
	b.WriteString(fmt.Sprintf(" -X main.BuildEnv %s", buildEnvironment()))
	return b.String()
}

func rmr(paths ...string) {
	for _, path := range paths {
		log.Println("rm -r", path)
		os.RemoveAll(path)
	}
}

func getReleaseVersion() (string, error) {
	fd, err := os.Open("RELEASE")
	if err != nil {
		return "", err
	}
	defer fd.Close()

	bs, err := ioutil.ReadAll(fd)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(bs)), nil
}

func getGitVersion() (string, error) {
	v, err := runError("git", "describe", "--always", "--dirty")
	if err != nil {
		return "", err
	}
	v = versionRe.ReplaceAllFunc(v, func(s []byte) []byte {
		s[0] = '+'
		return s
	})
	return string(v), nil
}

func getVersion() string {
	// First try for a RELEASE file,
	if ver, err := getReleaseVersion(); err == nil {
		return ver
	}
	// ... then see if we have a Git tag.
	if ver, err := getGitVersion(); err == nil {
		return ver
	}
	// This seems to be a dev build.
	return "unknown-dev"
}

func buildStamp() int64 {
	bs, err := runError("git", "show", "-s", "--format=%ct")
	if err != nil {
		return time.Now().Unix()
	}
	s, _ := strconv.ParseInt(string(bs), 10, 64)
	return s
}

func buildUser() string {
	u, err := user.Current()
	if err != nil {
		return "unknown-user"
	}
	return strings.Replace(u.Username, " ", "-", -1)
}

func buildHost() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown-host"
	}
	return h
}

func buildEnvironment() string {
	if v := os.Getenv("ENVIRONMENT"); len(v) > 0 {
		return v
	}
	return "default"
}

func buildArch() string {
	os := goos
	if os == "darwin" {
		os = "macosx"
	}
	return fmt.Sprintf("%s-%s", os, goarch)
}

func archiveName() string {
	return fmt.Sprintf("syncthing-%s-%s", buildArch(), version)
}

func run(cmd string, args ...string) []byte {
	bs, err := runError(cmd, args...)
	if err != nil {
		log.Println(cmd, strings.Join(args, " "))
		log.Println(string(bs))
		log.Fatal(err)
	}
	return bytes.TrimSpace(bs)
}

func runError(cmd string, args ...string) ([]byte, error) {
	ecmd := exec.Command(cmd, args...)
	bs, err := ecmd.CombinedOutput()
	return bytes.TrimSpace(bs), err
}

func runPrint(cmd string, args ...string) {
	log.Println(cmd, strings.Join(args, " "))
	ecmd := exec.Command(cmd, args...)
	ecmd.Stdout = os.Stdout
	ecmd.Stderr = os.Stderr
	err := ecmd.Run()
	if err != nil {
		log.Fatal(err)
	}
}

func runPipe(file, cmd string, args ...string) {
	log.Println(cmd, strings.Join(args, " "), ">", file)
	fd, err := os.Create(file)
	if err != nil {
		log.Fatal(err)
	}
	ecmd := exec.Command(cmd, args...)
	ecmd.Stdout = fd
	ecmd.Stderr = os.Stderr
	err = ecmd.Run()
	if err != nil {
		log.Fatal(err)
	}
	fd.Close()
}

type archiveFile struct {
	src  string
	dst  string
	perm os.FileMode
}

func tarGz(out string, files []archiveFile) {
	fd, err := os.Create(out)
	if err != nil {
		log.Fatal(err)
	}

	gw := gzip.NewWriter(fd)
	tw := tar.NewWriter(gw)

	for _, f := range files {
		sf, err := os.Open(f.src)
		if err != nil {
			log.Fatal(err)
		}

		info, err := sf.Stat()
		if err != nil {
			log.Fatal(err)
		}
		h := &tar.Header{
			Name:    f.dst,
			Size:    info.Size(),
			Mode:    int64(info.Mode()),
			ModTime: info.ModTime(),
		}

		err = tw.WriteHeader(h)
		if err != nil {
			log.Fatal(err)
		}
		_, err = io.Copy(tw, sf)
		if err != nil {
			log.Fatal(err)
		}
		sf.Close()
	}

	err = tw.Close()
	if err != nil {
		log.Fatal(err)
	}
	err = gw.Close()
	if err != nil {
		log.Fatal(err)
	}
	err = fd.Close()
	if err != nil {
		log.Fatal(err)
	}
}

func zipFile(out string, files []archiveFile) {
	fd, err := os.Create(out)
	if err != nil {
		log.Fatal(err)
	}

	zw := zip.NewWriter(fd)

	for _, f := range files {
		sf, err := os.Open(f.src)
		if err != nil {
			log.Fatal(err)
		}

		info, err := sf.Stat()
		if err != nil {
			log.Fatal(err)
		}

		fh, err := zip.FileInfoHeader(info)
		if err != nil {
			log.Fatal(err)
		}
		fh.Name = f.dst
		fh.Method = zip.Deflate

		if strings.HasSuffix(f.dst, ".txt") {
			// Text file. Read it and convert line endings.
			bs, err := ioutil.ReadAll(sf)
			if err != nil {
				log.Fatal(err)
			}
			bs = bytes.Replace(bs, []byte{'\n'}, []byte{'\n', '\r'}, -1)
			fh.UncompressedSize = uint32(len(bs))
			fh.UncompressedSize64 = uint64(len(bs))

			of, err := zw.CreateHeader(fh)
			if err != nil {
				log.Fatal(err)
			}
			of.Write(bs)
		} else {
			// Binary file. Copy verbatim.
			of, err := zw.CreateHeader(fh)
			if err != nil {
				log.Fatal(err)
			}
			_, err = io.Copy(of, sf)
			if err != nil {
				log.Fatal(err)
			}
		}
	}

	err = zw.Close()
	if err != nil {
		log.Fatal(err)
	}
	err = fd.Close()
	if err != nil {
		log.Fatal(err)
	}
}

func md5File(file string) error {
	fd, err := os.Open(file)
	if err != nil {
		return err
	}
	defer fd.Close()

	h := md5.New()
	_, err = io.Copy(h, fd)
	if err != nil {
		return err
	}

	out, err := os.Create(file + ".md5")
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "%x\n", h.Sum(nil))
	if err != nil {
		return err
	}

	return out.Close()
}

func vet(pkg string) {
	bs, err := runError("go", "vet", pkg)
	if err != nil && err.Error() == "exit status 3" || bytes.Contains(bs, []byte("no such tool \"vet\"")) {
		// Go said there is no go vet
		log.Println(`- No go vet, no vetting. Try "go get -u golang.org/x/tools/cmd/vet".`)
		return
	}

	falseAlarmComposites := regexp.MustCompile("composite literal uses unkeyed fields")
	exitStatus := regexp.MustCompile("exit status 1")
	for _, line := range bytes.Split(bs, []byte("\n")) {
		if falseAlarmComposites.Match(line) || exitStatus.Match(line) {
			continue
		}
		log.Printf("%s", line)
	}
}

func lint(pkg string) {
	bs, err := runError("golint", pkg)
	if err != nil {
		log.Println(`- No golint, not linting. Try "go get -u github.com/golang/lint/golint".`)
		return
	}

	analCommentPolicy := regexp.MustCompile(`exported (function|method|const|type|var) [^\s]+ should have comment`)
	for _, line := range bytes.Split(bs, []byte("\n")) {
		if analCommentPolicy.Match(line) {
			continue
		}
		log.Printf("%s", line)
	}
}
