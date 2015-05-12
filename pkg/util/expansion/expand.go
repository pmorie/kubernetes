/*
Copyright 2014 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package expansion

const (
	operator        = '$'
	openExpression  = '('
	closeExpression = ')'
	minimumExprLen  = 3
)

func syntaxWrap(input string) string {
	return string(operator) + string(openExpression) + input + string(closeExpression)
}

func MappingFuncFor(context ...map[string]string) func(string) string {
	return func(input string) string {
		for _, vars := range context {
			val, ok := vars[input]
			if ok {
				return val
			}
		}

		return syntaxWrap(input)
	}
}

// Expand replaces $(var) in the string based on the mapping function.
//
// TODO: Doc
func Expand(input string, mapping func(string) string) string {
	buf := make([]byte, 0, 2*len(input))
	checkpoint := 0
	for cursor := 0; cursor < len(input); cursor++ {
		if input[cursor] == operator && cursor+minimumExprLen < len(input) {
			// Copy the portion of the input string since the last
			// checkpoint into the buffer
			buf = append(buf, input[checkpoint:cursor]...)

			// Attempt to read the variable name as defined by the
			// syntax from the input string
			read, advance := tryReadVariableName(input[cursor+1:])

			// A read beginning with the operator is a passthrough
			// where the operator is not meaningful
			if len(read) > 0 && read[0] != operator {
				// Apply the mapping to the variable name and copy the
				// bytes into the buffer
				buf = append(buf, mapping(read)...)
			} else {
				// Pass-through; copy the read bytes into the buffer
				buf = append(buf, read...)
			}

			// Advance the cursor in the input string to account for
			// bytes consumed to read the variable name expression
			cursor += advance

			// Advance the checkpoint in the input string
			checkpoint = cursor + 1
		}
	}

	// Return the buffer and any remaining unwritten bytes in the
	// input string.
	return string(buf) + input[checkpoint:]
}

// TODO: doc
func tryReadVariableName(s string) (string, int) {
	switch s[0] {
	case operator:
		// Escaped operator; return it.
		return s[0:1], 1
	case openExpression:
		// Scan to expression closer
		for i := 1; i < len(s); i++ {
			if s[i] == closeExpression {
				return s[1:i], i + 1
			}
		}

		// Malformed expression; consume the expression opener
		return "", 1
	default:
		// Not the beginning of an expression, ie, an operator
		// that doesn't begin an expression.  Return the operator
		// and the first run in the string.
		return (string(operator) + string(s[0])), 1
	}
}
