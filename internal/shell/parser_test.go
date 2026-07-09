package shell

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"spaces only", "   \t ", nil, false},
		{"simple", "echo hello", []string{"echo", "hello"}, false},
		{"extra spaces", "  echo   hello  ", []string{"echo", "hello"}, false},
		{"trailing newline", "echo hi\n", []string{"echo", "hi"}, false},
		{"double quotes", `echo "hello world"`, []string{"echo", "hello world"}, false},
		{"single quotes", `echo 'a b c'`, []string{"echo", "a b c"}, false},
		{"windows path", `cd C:\Users\asc`, []string{"cd", `C:\Users\asc`}, false},
		{"quoted empty", `echo ""`, []string{"echo", ""}, false},
		{"adjacent quote", `a"b c"d`, []string{"ab cd"}, false},
		{"unclosed double", `echo "oops`, nil, true},
		{"unclosed single", `echo 'oops`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) err=%v wantErr=%v", tt.in, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Parse(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}
