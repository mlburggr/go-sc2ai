package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	git "github.com/go-git/go-git/v5"
)

var debug = flag.Bool("debug", false, "preserve temp proto files for inspection")

func main() {
	flag.Parse()

	// Checkout a temp copy of the API files
	dir, err := os.MkdirTemp("", "s2client-proto")
	check(err)
	defer os.RemoveAll(dir)

	if *debug {
		// Pause before deleting modified proto files
		fmt.Println("Proto files in:", dir)
		defer func() {
			if err := recover(); err != nil {
				fmt.Println(err)
			}
			fmt.Print("Press Enter to continue...")
			b := make([]byte, 1)
			os.Stdin.Read(b)
		}()
	}

	_, err = git.PlainClone(dir, false, &git.CloneOptions{
		URL:      "https://github.com/Blizzard/s2client-proto",
		Progress: os.Stdout,
	})
	check(err)

	// Get all the .proto files
	protoDir := filepath.Join(dir, "s2clientprotocol")
	files, err := os.ReadDir(protoDir)
	check(err)

	protocArgs := []string{
		"--proto_path=" + protoDir,
		"--go_out=.", "--go_opt=module=github.com/chippydip/go-sc2ai",
		"--go-vtproto_out=.", "--go-vtproto_opt=module=github.com/chippydip/go-sc2ai,features=marshal+unmarshal+size",
	}
	for _, file := range files {
		if filepath.Ext(file.Name()) != ".proto" {
			continue
		}
		path := filepath.Join(protoDir, file.Name())

		// Upgrade the file to proto3 and fix the package name
		upgradeProto(path)

		// Add the file to the list of command line args for protoc
		protocArgs = append(protocArgs, path)
	}

	// Generate go code from the .proto files
	fmt.Println("protoc " + strings.Join(protocArgs, " "))
	fmt.Println()
	out, err := exec.Command("protoc", protocArgs...).CombinedOutput()
	if len(out) > 0 {
		fmt.Println(string(out))
	}
	check(err)

	// Post-process generated files: add semantic types, fix enum naming,
	// and strip protoimpl internals for vtprotobuf-only usage.
	postProcessGeneratedFiles()

	// Generate typed wrappers for ImageData fields in spatial.pb.go.
	generateTypedImageData()

	// Format all generated files.
	out, err = exec.Command("go", "fmt", "./api/...").CombinedOutput()
	if len(out) > 0 {
		fmt.Print(string(out))
	}
	check(err)
}

// upgradeProto converts a proto2 file to proto3 and sets the Go package option.
func upgradeProto(path string) {
	content, err := os.ReadFile(path)
	check(err)
	text := string(content)

	// Upgrade syntax and set Go package
	text = strings.Replace(text, `syntax = "proto2";`,
		"syntax = \"proto3\";\noption go_package = \"github.com/chippydip/go-sc2ai/api\";", 1)

	// Fix import paths (remove s2clientprotocol/ subdirectory)
	text = strings.ReplaceAll(text, `import "s2clientprotocol/`, `import "`)

	// Remove "optional" qualifier (implicit in proto3)
	optionalRe := regexp.MustCompile(`(?m)^(\s*)optional `)
	text = optionalRe.ReplaceAllString(text, "$1")

	// Add zero values for enums (required in proto3; Race and CloakState already have them)
	enumRe := regexp.MustCompile(`(?m)^(\s*)enum (\w+) \{`)
	text = enumRe.ReplaceAllStringFunc(text, func(match string) string {
		groups := enumRe.FindStringSubmatch(match)
		name := groups[2]
		if name == "Race" || name == "CloakState" {
			return match
		}
		return match + "\n" + groups[1] + "  " + name + "_nil = 0;"
	})

	check(os.WriteFile(path, []byte(text), 0644))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
