// Command zipppmd lists and extracts ZIP archives that use PPMd
// compression (method 98), as well as plain store/deflate archives.
//
// Usage:
//
//	zipppmd list [-v] archive.zip...
//	zipppmd extract [-d dir] archive.zip [patterns...]
//	zipppmd create archive.zip files... (not implemented yet)
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/MrOzean/go-zip-ppmd"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "list", "ls":
		cmdList(os.Args[2:])
	case "extract", "x":
		cmdExtract(os.Args[2:])
	case "create", "c":
		fmt.Fprintln(os.Stderr, "zipppmd: create is not implemented yet (PPMd compression is planned)")
		os.Exit(1)
	case "version", "--version", "-version":
		fmt.Println(zipppmd.Version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  zipppmd list [-v] archive.zip...
  zipppmd extract [-d dir] archive.zip [patterns...]
  zipppmd create archive.zip files... (not implemented yet)
  zipppmd version`)
}

func openReader(path string) (*zipppmd.Reader, *os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	r, err := zipppmd.NewReader(f, st.Size())
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return r, f, nil
}

func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	verbose := fs.Bool("v", false, "verbose output")
	fs.Parse(args)
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "zipppmd list: no archive given")
		os.Exit(2)
	}
	for _, path := range fs.Args() {
		r, f, err := openReader(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "zipppmd: %s: %v\n", path, err)
			os.Exit(1)
		}
		if len(fs.Args()) > 1 {
			fmt.Printf("==> %s <==\n", path)
		}
		for _, file := range r.File {
			method := methodName(file.Method)
			if *verbose {
				fmt.Printf("%10d %10d %6.1f%%  %s  %08x  %s\n",
					file.UncompressedSize, file.CompressedSize,
					ratio(file.CompressedSize, file.UncompressedSize),
					method, file.CRC32, file.Name)
			} else {
				fmt.Printf("%10d  %s  %s\n", file.UncompressedSize, method, file.Name)
			}
		}
		f.Close()
	}
}

func cmdExtract(args []string) {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	dir := fs.String("d", ".", "destination directory")
	fs.Parse(args)
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "zipppmd extract: no archive given")
		os.Exit(2)
	}
	patterns := fs.Args()[1:]

	var sr *zipppmd.StreamReader
	if fs.Arg(0) == "-" {
		sr = zipppmd.NewStreamReader(os.Stdin)
	} else {
		f, err := os.Open(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "zipppmd: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		sr = zipppmd.NewStreamReader(f)
	}
	var total int64
	count := 0
	for {
		entry, err := sr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "zipppmd: %v\n", err)
			os.Exit(1)
		}
		if !matchAny(entry.Name, patterns) {
			continue
		}
		if entry.IsDir() {
			continue
		}
		rc, err := sr.Open()
		if err != nil {
			fmt.Fprintf(os.Stderr, "zipppmd: %s: %v\n", entry.Name, err)
			os.Exit(1)
		}
		dest := filepath.Join(*dir, filepath.FromSlash(entry.Name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "zipppmd: %v\n", err)
			os.Exit(1)
		}
		out, err := os.Create(dest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "zipppmd: %v\n", err)
			os.Exit(1)
		}
		n, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			fmt.Fprintf(os.Stderr, "zipppmd: %s: %v\n", entry.Name, copyErr)
			os.Exit(1)
		}
		if closeErr != nil {
			fmt.Fprintf(os.Stderr, "zipppmd: %s: %v\n", entry.Name, closeErr)
			os.Exit(1)
		}
		total += n
		count++
		fmt.Printf("%10d  %s\n", n, entry.Name)
	}
	fmt.Printf("extracted %d files, %d bytes\n", count, total)
}

func methodName(m uint16) string {
	switch m {
	case zipppmd.MethodStore:
		return "store"
	case zipppmd.MethodDeflate:
		return "deflate"
	case zipppmd.MethodPPMd:
		return "ppmd"
	default:
		return fmt.Sprintf("%d", m)
	}
}

func ratio(a, b uint64) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func matchAny(name string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if ok, err := filepath.Match(p, name); err == nil && ok {
			return true
		}
		if strings.HasPrefix(name, strings.TrimSuffix(p, "*")) {
			return true
		}
	}
	return false
}
