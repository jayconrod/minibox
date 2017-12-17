// +build ignore

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	log.SetFlags(0)

	fmt.Println("invoked as: ", strings.Join(os.Args, " "))
	fmt.Printf("\nuid: %d gid: %d\n", os.Getuid(), os.Getgid())
	fmt.Print("\nenvironment:\n")
	for _, e := range os.Environ() {
		fmt.Println(e)
	}
	fmt.Print("\nfiles:\n")
	filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	})
}
