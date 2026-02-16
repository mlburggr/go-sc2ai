package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	git "github.com/go-git/go-git/v5"
)

func main() {
	// Save typeMap for post-processing (mapTypes deletes entries for validation)
	allTypes := make(map[string]string, len(typeMap))
	for k, v := range typeMap {
		allTypes[k] = v
	}

	// Checkout a temp copy of the API files
	dir, err := os.MkdirTemp("", "s2client-proto")
	check(err)
	defer os.RemoveAll(dir)

	// // Preserve the test directory to look at
	// defer func() {
	// 	if err := recover(); err != nil {
	// 		fmt.Println(err)
	// 	}

	// 	fmt.Print("Press 'Enter' to continue...")
	// 	bufio.NewReader(os.Stdin).ReadBytes('\n')
	// }()

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
		writeLines(path, upgradeProto(path))

		// Add the file to the list of command line args for protoc
		protocArgs = append(protocArgs, path)
	}

	// Make sure we mapped all the expected types
	if len(typeMap) != 0 {
		fmt.Println("Not all types were mapped, missing:")
		for key := range typeMap {
			fmt.Println(key)
		}
	}

	// Generate go code from the .proto files
	fmt.Println("protoc " + strings.Join(protocArgs, " ") + "\n\n")
	out, err := exec.Command("protoc", protocArgs...).CombinedOutput()
	fmt.Println(string(out) + "\n\n")
	check(err)

	// Post-process generated files: add semantic types and fix enum naming
	rewriteGeneratedFiles(allTypes)

	// Strip protoimpl internals (state, sizeCache, unknownFields) from all message types.
	// We use vtprotobuf exclusively, so the standard proto reflection machinery is unnecessary.
	// This makes simple types like Point2D comparable and eliminates mutex-copy warnings.
	stripProtoInternals()
}

// Thing we want to use twice
const (
	importPrefix   = "import \"s2clientprotocol/"
	optionalPrefix = "optional "
	enumPrefix     = "enum "
	messagePrefix  = "message "
)

func upgradeProto(path string) []string {
	file, err := os.Open(path)
	check(err)
	defer file.Close()

	propPath := []string{}
	var lines []string

	// Read line by line, making modifications as needed
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		// Get the line and trim comments and whitespace to make matching easier
		line := scanner.Text()
		if comment := strings.Index(line, "//"); comment > 0 {
			line = line[:comment]
		}
		line = strings.TrimSpace(line)

		switch {
		// Upgrade to proto3 and set the go package name
		case line == "syntax = \"proto2\";":
			lines = append(lines, "syntax = \"proto3\";", "option go_package = \"github.com/chippydip/go-sc2ai/api\";")

		// Remove subdirectory of the import so the output path isn't nested
		case strings.HasPrefix(line, importPrefix):
			lines = append(lines, "import \""+line[len(importPrefix):])

		// Remove "optional" prefixes (they are implicit in proto3)
		case strings.HasPrefix(line, optionalPrefix):
			lines = append(lines, mapTypes(propPath, line[len(optionalPrefix):]))

		// Track where we are in the path
		case strings.HasSuffix(line, " {"):
			id := strings.Split(line, " ")[1] // "<type> Identifier {"
			propPath = append(propPath, id)

			lines = append(lines, line)

			// Enums must have a zero value in proto3 (and unfortunately they must be unique due to C++ scoping rules)
			if strings.HasPrefix(line, enumPrefix) && line != "enum Race {" && line != "enum CloakState {" {
				lines = append(lines, line[len(enumPrefix):len(line)-2]+"_nil = 0;")
			}

		// Pop the last path element
		case line == "}":
			if propPath[len(propPath)-1] == "Unit" {
				lines = append(lines,
					"repeated AvailableAbility actions = 100;",
				)
			}
			propPath = propPath[:len(propPath)-1]
			lines = append(lines, line)

		// Everything else just gets copied to the output
		default:
			lines = append(lines, mapTypes(propPath, line))
		}
	}

	return lines
}

func mapTypes(path []string, line string) string {
	parts := strings.Split(line, " ")
	if len(parts) < 4 {
		return line // need at least "<type> <name> = <num>;"
	}

	key := strings.Join(path, ".") + "." + parts[len(parts)-3]
	delete(typeMap, key) // track which ones have been processed (no-op if not present)

	return line
}

func writeLines(path string, lines []string) {
	file, err := os.Create(path)
	check(err)
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, line := range lines {
		fmt.Fprintln(writer, line)
	}
	check(writer.Flush())
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

// Make the API more type-safe
var typeMap = map[string]string{
	// common.proto
	"AvailableAbility.ability_id": "AbilityID",
	// data.proto
	"AbilityData.ability_id":           "AbilityID",
	"AbilityData.remaps_to_ability_id": "AbilityID",
	"UnitTypeData.unit_id":             "UnitTypeID",
	"UnitTypeData.ability_id":          "AbilityID",
	"UnitTypeData.tech_alias":          "UnitTypeID",
	"UnitTypeData.unit_alias":          "UnitTypeID",
	"UnitTypeData.tech_requirement":    "UnitTypeID",
	"UpgradeData.upgrade_id":           "UpgradeID",
	"UpgradeData.ability_id":           "AbilityID",
	"BuffData.buff_id":                 "BuffID",
	"EffectData.effect_id":             "EffectID",
	// debug.proto
	"DebugCreateUnit.unit_type":  "UnitTypeID",
	"DebugCreateUnit.owner":      "PlayerID",
	"DebugKillUnit.tag":          "UnitTag",
	"DebugSetUnitValue.unit_tag": "UnitTag",
	// error.proto
	// query.proto
	"RequestQueryPathing.start.unit_tag":             "UnitTag",
	"RequestQueryAvailableAbilities.unit_tag":        "UnitTag",
	"ResponseQueryAvailableAbilities.unit_tag":       "UnitTag",
	"ResponseQueryAvailableAbilities.unit_type_id":   "UnitTypeID",
	"RequestQueryBuildingPlacement.ability_id":       "AbilityID",
	"RequestQueryBuildingPlacement.placing_unit_tag": "UnitTag",
	// raw.proto
	"PowerSource.tag":                             "UnitTag",
	"PlayerRaw.upgrade_ids":                       "UpgradeID",
	"UnitOrder.ability_id":                        "AbilityID",
	"UnitOrder.target.target_unit_tag":            "UnitTag",
	"PassengerUnit.tag":                           "UnitTag",
	"PassengerUnit.unit_type":                     "UnitTypeID",
	"Unit.tag":                                    "UnitTag",
	"Unit.unit_type":                              "UnitTypeID",
	"Unit.owner":                                  "PlayerID",
	"Unit.add_on_tag":                             "UnitTag",
	"Unit.buff_ids":                               "BuffID",
	"Unit.engaged_target_tag":                     "UnitTag",
	"Event.dead_units":                            "UnitTag",
	"Effect.effect_id":                            "EffectID",
	"ActionRawUnitCommand.ability_id":             "AbilityID",
	"ActionRawUnitCommand.target.target_unit_tag": "UnitTag",
	"ActionRawUnitCommand.unit_tags":              "UnitTag",
	"ActionRawToggleAutocast.ability_id":          "AbilityID",
	"ActionRawToggleAutocast.unit_tags":           "UnitTag",
	// sc2api.proto
	"RequestJoinGame.participation.observed_player_id": "PlayerID",
	"ResponseJoinGame.player_id":                       "PlayerID",
	"RequestStartReplay.observed_player_id":            "PlayerID",
	"ChatReceived.player_id":                           "PlayerID",
	"PlayerInfo.player_id":                             "PlayerID",
	"PlayerCommon.player_id":                           "PlayerID",
	"ActionError.unit_tag":                             "UnitTag",
	"ActionError.ability_id":                           "AbilityID",
	"ActionObserverPlayerPerspective.player_id":        "PlayerID",
	"ActionObserverCameraFollowPlayer.player_id":       "PlayerID",
	"ActionObserverCameraFollowUnits.unit_tags":        "UnitTag",
	"PlayerResult.player_id":                           "PlayerID",
	// score.proto
	// spatial.proto
	// ui.proto
	"ControlGroup.leader_unit_type":   "UnitTypeID",
	"UnitInfo.unit_type":              "UnitTypeID",
	"UnitInfo.player_relative":        "PlayerID", // TODO: is this correct?
	"BuildItem.ability_id":            "AbilityID",
	"ActionToggleAutocast.ability_id": "AbilityID",
}

// TODO: spatial.proto?

// snakeToCamel converts a snake_case proto field name to CamelCase Go field name.
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

// rewriteGeneratedFiles post-processes protoc output to replace primitive
// types with semantic types (AbilityID, UnitTag, etc.) and fix enum naming.
func rewriteGeneratedFiles(allTypes map[string]string) {
	// Build goFieldName → targetType lookup.
	// Every Go field name maps to exactly one target type across all structs,
	// so we can do global replacements without scoping to specific structs.
	goFieldTargets := map[string]string{}
	for key, target := range allTypes {
		parts := strings.Split(key, ".")
		protoField := parts[len(parts)-1]
		goField := snakeToCamel(protoField)
		if existing, ok := goFieldTargets[goField]; ok && existing != target {
			panic(fmt.Sprintf("conflicting targets for %s: %s vs %s", goField, existing, target))
		}
		goFieldTargets[goField] = target
	}

	files, err := filepath.Glob(filepath.Join("api", "*.pb.go"))
	check(err)

	// First pass: rewrite types in .pb.go and _vtproto.pb.go files,
	// and collect enum renames from .pb.go files.
	enumRenames := map[string]string{} // old const name → new const name
	for _, f := range files {
		if strings.HasSuffix(f, "_vtproto.pb.go") {
			rewriteVtprotoFile(f, goFieldTargets)
		} else {
			rewritePbGoFile(f, goFieldTargets, enumRenames)
		}
	}

	// Second pass: apply enum renames across ALL .pb.go files (cross-file references).
	if len(enumRenames) > 0 {
		for _, f := range files {
			if strings.HasSuffix(f, "_vtproto.pb.go") {
				continue
			}
			content, err := os.ReadFile(f)
			check(err)
			text := string(content)
			original := text
			for oldName, newName := range enumRenames {
				text = strings.ReplaceAll(text, oldName, newName)
			}
			if text != original {
				check(os.WriteFile(f, []byte(text), 0644))
				fmt.Printf("Renamed enums in %s\n", f)
			}
		}
	}
}

// isNumericType returns true for Go numeric types that we want to replace with semantic types.
func isNumericType(t string) bool {
	base := strings.TrimPrefix(t, "[]")
	switch base {
	case "uint32", "uint64", "int32", "int64":
		return true
	}
	return false
}

// rewritePbGoFile rewrites struct field types, getter return types, and
// double-prefix enum nil constants in a generated .pb.go file.
func rewritePbGoFile(filename string, goFieldTargets map[string]string, enumRenames map[string]string) {
	content, err := os.ReadFile(filename)
	check(err)
	text := string(content)
	original := text

	for goField, target := range goFieldTargets {
		// Rewrite struct field types.
		// Matches: \t<GoField>  <type>  `protobuf:
		fieldRe := regexp.MustCompile(
			`(\t` + regexp.QuoteMeta(goField) + `\s+)((?:\[\])?\w+)(\s+` + "`" + `protobuf:)`)
		text = fieldRe.ReplaceAllStringFunc(text, func(match string) string {
			groups := fieldRe.FindStringSubmatch(match)
			oldType := groups[2]
			if !isNumericType(oldType) {
				return match // don't rewrite bool, string, enum, message fields
			}
			newType := target
			if strings.HasPrefix(oldType, "[]") {
				newType = "[]" + target
			}
			return groups[1] + newType + groups[3]
		})

		// Rewrite getter return types.
		// Matches: func (x *Struct) Get<GoField>() <type> {
		getterRe := regexp.MustCompile(
			`(func \(x \*\w+\) Get` + regexp.QuoteMeta(goField) + `\(\) )((?:\[\])?\w+)( \{)`)
		text = getterRe.ReplaceAllStringFunc(text, func(match string) string {
			groups := getterRe.FindStringSubmatch(match)
			oldType := groups[2]
			if !isNumericType(oldType) {
				return match
			}
			newType := target
			if strings.HasPrefix(oldType, "[]") {
				newType = "[]" + target
			}
			return groups[1] + newType + groups[3]
		})
	}

	// Collect double-prefix enum nil constants for cross-file renaming.
	// protoc-gen-go generates Status_Status_nil for top-level enum Status with
	// value Status_nil. We rename to Status_nil for backward compat.
	nilConstRe := regexp.MustCompile(`\t(\w+_nil)\s+(\w+)\s*=\s*0\b`)
	for _, match := range nilConstRe.FindAllStringSubmatch(text, -1) {
		constName := match[1]
		typeName := match[2]
		doublePrefix := typeName + "_" + typeName + "_nil"
		if constName == doublePrefix {
			enumRenames[constName] = typeName + "_nil"
		}
	}

	if text != original {
		check(os.WriteFile(filename, []byte(text), 0644))
		fmt.Printf("Rewrote %s\n", filename)
	}
}

// stripProtoInternals removes state, sizeCache, and unknownFields from all
// generated message types. These fields are only needed by the standard protobuf
// reflection API; vtprotobuf methods access data fields directly.
func stripProtoInternals() {
	files, err := filepath.Glob(filepath.Join("api", "*.pb.go"))
	check(err)

	for _, f := range files {
		if strings.HasSuffix(f, "_vtproto.pb.go") {
			stripVtprotoUnknownFields(f)
		} else {
			stripPbGoInternals(f)
		}
	}
}

// stripPbGoInternals strips state/sizeCache/unknownFields fields from structs
// and simplifies Reset() and ProtoReflect() to not use MessageStateOf.
func stripPbGoInternals(filename string) {
	content, err := os.ReadFile(filename)
	check(err)
	text := string(content)
	original := text

	// Strip struct fields
	stateRe := regexp.MustCompile(`\tstate\s+protoimpl\.MessageState[^\n]*\n`)
	unknownRe := regexp.MustCompile(`\tunknownFields\s+protoimpl\.UnknownFields\n`)
	sizeCacheRe := regexp.MustCompile(`\tsizeCache\s+protoimpl\.SizeCache\n`)
	text = stateRe.ReplaceAllString(text, "")
	text = unknownRe.ReplaceAllString(text, "")
	text = sizeCacheRe.ReplaceAllString(text, "")

	// Simplify Reset(): remove MessageStateOf lines after *x = Type{}
	resetRe := regexp.MustCompile(
		`\tmi := &file_\w+_proto_msgTypes\[\d+\]\n` +
			`\tms := protoimpl\.X\.MessageStateOf\(protoimpl\.Pointer\(x\)\)\n` +
			`\tms\.StoreMessageInfo\(mi\)\n`)
	text = resetRe.ReplaceAllString(text, "")

	// Simplify ProtoReflect(): replace cached MessageStateOf path with direct MessageOf
	protoReflectRe := regexp.MustCompile(
		`(func \(x \*\w+\) ProtoReflect\(\) protoreflect\.Message \{\n)` +
			`\tmi := &(file_\w+_proto_msgTypes\[\d+\])\n` +
			`\tif x != nil \{\n` +
			`\t\tms := protoimpl\.X\.MessageStateOf\(protoimpl\.Pointer\(x\)\)\n` +
			`\t\tif ms\.LoadMessageInfo\(\) == nil \{\n` +
			`\t\t\tms\.StoreMessageInfo\(mi\)\n` +
			`\t\t\}\n` +
			`\t\treturn ms\n` +
			`\t\}\n` +
			`\treturn mi\.MessageOf\(x\)\n` +
			`\}`)
	text = protoReflectRe.ReplaceAllString(text, "${1}\treturn ${2}.MessageOf(x)\n}")

	if text != original {
		check(os.WriteFile(filename, []byte(text), 0644))
		fmt.Printf("Stripped proto internals from %s\n", filename)
	}
}

// stripVtprotoUnknownFields removes all unknownFields references from vtproto
// marshal, size, and unmarshal code.
func stripVtprotoUnknownFields(filename string) {
	content, err := os.ReadFile(filename)
	check(err)
	text := string(content)
	original := text

	// Marshal: remove the unknownFields copy block
	marshalRe := regexp.MustCompile(
		`\tif m\.unknownFields != nil \{\n` +
			`\t\ti -= len\(m\.unknownFields\)\n` +
			`\t\tcopy\(dAtA\[i:\], m\.unknownFields\)\n` +
			`\t\}\n`)
	text = marshalRe.ReplaceAllString(text, "")

	// Size: remove the unknownFields length addition
	sizeRe := regexp.MustCompile(`\tn \+= len\(m\.unknownFields\)\n`)
	text = sizeRe.ReplaceAllString(text, "")

	// Unmarshal: remove the unknownFields append line
	unmarshalRe := regexp.MustCompile(`\t\t\tm\.unknownFields = append\(m\.unknownFields, dAtA\[iNdEx:iNdEx\+skippy\]\.\.\.\)\n`)
	text = unmarshalRe.ReplaceAllString(text, "")

	if text != original {
		check(os.WriteFile(filename, []byte(text), 0644))
		fmt.Printf("Stripped unknownFields from %s\n", filename)
	}
}

// rewriteVtprotoFile rewrites type casts in unmarshal code to use semantic types.
func rewriteVtprotoFile(filename string, goFieldTargets map[string]string) {
	content, err := os.ReadFile(filename)
	check(err)

	lines := strings.Split(string(content), "\n")
	changed := false

	castRe := regexp.MustCompile(`(\w+)\(b&0x7F\)`)
	makeRe := regexp.MustCompile(`make\(\[\](\w+),`)

	for goField, target := range goFieldTargets {
		// Find case blocks by the error message that identifies the field.
		errorPattern := `for field ` + goField + `"`
		for i, line := range lines {
			if !strings.Contains(line, errorPattern) {
				continue
			}

			// Find case block boundaries (walk backward to case start, forward to next case).
			caseStart := 0
			for j := i - 1; j >= 0; j-- {
				t := strings.TrimSpace(lines[j])
				if (strings.HasPrefix(t, "case ") && strings.HasSuffix(t, ":")) ||
					strings.HasPrefix(t, "func ") {
					caseStart = j + 1
					break
				}
			}
			caseEnd := len(lines)
			for j := i + 1; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if (strings.HasPrefix(t, "case ") && strings.HasSuffix(t, ":")) ||
					strings.HasPrefix(t, "func ") {
					caseEnd = j
					break
				}
			}

			// Apply replacements within the case block.
			for j := caseStart; j < caseEnd; j++ {
				newLine := lines[j]
				trimmed := strings.TrimSpace(newLine)

				// m.GoField |= <type>(b&0x7F) << shift
				if strings.Contains(newLine, "m."+goField) && strings.Contains(newLine, "(b&0x7F)") {
					newLine = castRe.ReplaceAllString(newLine, target+"(b&0x7F)")
				}

				// var v <type> (repeated/oneof fields use a local variable)
				if strings.HasPrefix(trimmed, "var v ") && !strings.Contains(trimmed, "=") {
					indent := newLine[:len(newLine)-len(strings.TrimLeft(newLine, "\t "))]
					newLine = indent + "var v " + target
				}

				// v |= <type>(b&0x7F) << shift
				if strings.HasPrefix(trimmed, "v |=") && strings.Contains(newLine, "(b&0x7F)") {
					newLine = castRe.ReplaceAllString(newLine, target+"(b&0x7F)")
				}

				// make([]<type>, 0, elementCount)
				if strings.Contains(newLine, "m."+goField) && strings.Contains(newLine, "make([]") {
					newLine = makeRe.ReplaceAllString(newLine, "make([]"+target+",")
				}

				if newLine != lines[j] {
					lines[j] = newLine
					changed = true
				}
			}
		}
	}

	if changed {
		check(os.WriteFile(filename, []byte(strings.Join(lines, "\n")), 0644))
		fmt.Printf("Rewrote %s\n", filename)
	}
}
