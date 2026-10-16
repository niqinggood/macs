package processutil

import "strings"

// ParseCommandLine splits a command string into arguments while preserving
// whitespace inside single- and double-quoted values.
func ParseCommandLine(command string) []string {
	var args []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(command); i++ {
		char := command[i]
		switch {
		case char == '\'' && !inDouble:
			inSingle = !inSingle
		case char == '"' && !inSingle:
			inDouble = !inDouble
		case char == ' ' && !inSingle && !inDouble:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(char)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}
