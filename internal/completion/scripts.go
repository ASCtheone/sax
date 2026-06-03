package completion

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// Script returns the shell completion script for the given shell. Supported
// shells: "zsh", "bash", "pwsh" (alias "powershell"). An empty shell is
// auto-detected. The script wires up sax and nx completion (via `sax
// __complete` / `sax nx __complete`) and delegates to the native completion of
// git/npm/pnpm/yarn where the tool provides one.
func Script(shell string) (string, error) {
	if shell == "" {
		shell = DetectShell()
	}
	switch strings.ToLower(shell) {
	case "zsh":
		return zshScript, nil
	case "bash":
		return bashScript, nil
	case "pwsh", "powershell":
		return fmt.Sprintf(pwshScriptTmpl, EmptyWordSentinel), nil
	default:
		return "", fmt.Errorf("unsupported shell %q (use zsh, bash, or pwsh)", shell)
	}
}

// DetectShell guesses the user's interactive shell for completion setup.
func DetectShell() string {
	if runtime.GOOS == "windows" {
		return "pwsh"
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		switch {
		case strings.Contains(sh, "zsh"):
			return "zsh"
		case strings.Contains(sh, "bash"):
			return "bash"
		}
	}
	return "bash"
}

const bashScript = `# sax shell completion (bash)
_sax_complete() {
    local IFS=$'\n'
    COMPREPLY=( $(sax __complete "${COMP_WORDS[@]:1}") )
}
complete -o bashdefault -o default -F _sax_complete sax

_sax_nx_complete() {
    local IFS=$'\n'
    COMPREPLY=( $(sax nx __complete "${COMP_WORDS[@]:1}") )
}
complete -o bashdefault -o default -F _sax_nx_complete nx

# Native completions for related tools, where available.
command -v npm >/dev/null 2>&1 && eval "$(npm completion 2>/dev/null)" >/dev/null 2>&1
command -v pnpm >/dev/null 2>&1 && eval "$(pnpm completion bash 2>/dev/null)" >/dev/null 2>&1
# git completion is typically provided by the system's bash-completion package.
`

const zshScript = `# sax shell completion (zsh)
if ! (( $+functions[compdef] )); then
    autoload -Uz compinit && compinit
fi

_sax_complete() {
    local -a completions
    local IFS=$'\n'
    completions=($(sax __complete "${words[@]:1}"))
    compadd -- ${completions[@]}
}
compdef _sax_complete sax

_sax_nx_complete() {
    local -a completions
    local IFS=$'\n'
    completions=($(sax nx __complete "${words[@]:1}"))
    compadd -- ${completions[@]}
}
compdef _sax_nx_complete nx

# Native completions for related tools, where available.
command -v npm >/dev/null 2>&1 && eval "$(npm completion 2>/dev/null)" >/dev/null 2>&1
command -v pnpm >/dev/null 2>&1 && eval "$(pnpm completion zsh 2>/dev/null)" >/dev/null 2>&1
`

// pwshScriptTmpl has one %s placeholder for the empty-word sentinel. Windows
// PowerShell 5.1 drops empty-string arguments to native commands, so a trailing
// empty word is sent as the sentinel token, which sax converts back to "".
const pwshScriptTmpl = `# sax shell completion (pwsh)
function global:__sax_request($subcommand, $commandAst, $wordToComplete) {
    $elements = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { "$_" })
    if ([string]::IsNullOrEmpty($wordToComplete)) { $elements += '%s' }
    & sax @subcommand @elements | ForEach-Object {
        [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
    }
}
Register-ArgumentCompleter -CommandName sax -Native -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    __sax_request @('__complete') $commandAst $wordToComplete
}
Register-ArgumentCompleter -CommandName nx -Native -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    __sax_request @('nx', '__complete') $commandAst $wordToComplete
}
`
