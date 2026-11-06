package prefs

import (
	"errors"
	"fmt"
	"strings"
)

// recorderFlags are the gpu-screen-recorder options accepted as extra
// arguments. Options that decide the output, capture source, replay
// lifecycle or a value with its own preference are deliberately absent.
var recorderFlags = []string{
	"-ac", "-ab", "-fm", "-bm", "-cr", "-pixfmt", "-tune", "-keyint",
	"-low-power", "-fallback-cpu-encoding", "-v", "-gl-debug", "-exclude-metadata",
}

// SplitArgs splits s into words the way a POSIX shell would, honouring
// single quotes, double quotes and backslash escapes. Nothing is expanded
// and no shell is involved.
func SplitArgs(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	var quote rune
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			escaped, inWord = true, true
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if escaped {
		return nil, errors.New("trailing backslash")
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// RecorderArgs parses extra gpu-screen-recorder arguments. Each option must
// be one of the permitted flags followed by its value as a separate word;
// replay additionally rejects -bm, being pinned to a constant bitrate.
func RecorderArgs(s string, replay bool) ([]string, error) {
	words, err := SplitArgs(s)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(words); i += 2 {
		flag := words[i]
		allowed := false
		for _, f := range recorderFlags {
			if flag == f && (!replay || f != "-bm") {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("%q is not an option that may be added here", flag)
		}
		if i+1 >= len(words) {
			return nil, fmt.Errorf("%s needs a value", flag)
		}
		if v := words[i+1]; v == "" || strings.HasPrefix(v, "-") {
			return nil, fmt.Errorf("%s needs a value, not %q", flag, v)
		}
	}
	return words, nil
}

// RecorderFlags lists the options accepted by RecorderArgs, for help text.
func RecorderFlags(replay bool) []string {
	var out []string
	for _, f := range recorderFlags {
		if !replay || f != "-bm" {
			out = append(out, f)
		}
	}
	return out
}
