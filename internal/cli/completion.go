package cli

import (
	"flag"
	"fmt"
	"slices"

	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdCompletion prints a shell completion script. The scripts contain no
// command or flag lists of their own — they delegate every query to the
// hidden `friday __complete` callback, so completions can never drift from
// the binary that's actually installed.
func cmdCompletion(args []string) int {
	if len(args) != 1 {
		output.Err("usage: friday completion bash|zsh|fish")
		return 1
	}
	switch args[0] {
	case "bash":
		fmt.Print(bashCompletion)
	case "zsh":
		fmt.Print(zshCompletion)
	case "fish":
		fmt.Print(fishCompletion)
	default:
		output.Err("unsupported shell %q (want: bash, zsh, fish)", args[0])
		return 1
	}
	return 0
}

// cmdComplete is the hidden callback the scripts invoke. With no args (or a
// blank arg) it prints every command name; given a command name it prints
// that command's flags, subcommands, and — where positionals are adapter
// names — the live adapter list from the user's config.
func cmdComplete(args []string) int {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	for _, w := range completionsFor(cmd) {
		fmt.Println(w)
	}
	return 0
}

func completionsFor(cmd string) []string {
	table := commandTable()
	if cmd == "" {
		var names []string
		for _, c := range table {
			names = append(names, c.name)
			names = append(names, c.aliases...)
		}
		return names
	}
	for _, c := range table {
		if c.name != cmd && !slices.Contains(c.aliases, cmd) {
			continue
		}
		var words []string
		if c.flags != nil {
			fs := c.flags()
			fs.VisitAll(func(f *flag.Flag) {
				words = append(words, "--"+f.Name)
			})
		}
		words = append(words, c.subcommands...)
		if c.completesAdapters {
			if cfg, err := loadUserOrDefault(); err == nil {
				words = append(words, cfg.AdapterNames()...)
			}
		}
		return words
	}
	return nil
}

const bashCompletion = `# bash completion for friday — eval "$(friday completion bash)"
_friday() {
    local cur words
    cur="${COMP_WORDS[COMP_CWORD]}"
    if [ "$COMP_CWORD" -eq 1 ]; then
        words="$(friday __complete 2>/dev/null)"
    else
        words="$(friday __complete "${COMP_WORDS[1]}" 2>/dev/null)"
    fi
    COMPREPLY=($(compgen -W "$words" -- "$cur"))
}
complete -F _friday friday
`

const zshCompletion = `#compdef friday
# zsh completion for friday — friday completion zsh > "${fpath[1]}/_friday"
_friday() {
    local -a completions
    if (( CURRENT == 2 )); then
        completions=(${(f)"$(friday __complete 2>/dev/null)"})
    else
        completions=(${(f)"$(friday __complete "${words[2]}" 2>/dev/null)"})
    fi
    compadd -a completions
}
_friday "$@"
`

const fishCompletion = `# fish completion for friday — friday completion fish > ~/.config/fish/completions/friday.fish
function __friday_complete
    set -l cmd (commandline -opc)
    if test (count $cmd) -le 1
        friday __complete 2>/dev/null
    else
        friday __complete $cmd[2] 2>/dev/null
    end
end
complete -c friday -f -a "(__friday_complete)"
`
