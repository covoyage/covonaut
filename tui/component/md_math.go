package component

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// latexToUnicode is a small, lossy mapping of common TeX tokens to Unicode
// so block/inline math is readable in a terminal. It is not a TeX engine.
// Longer commands must appear before shorter prefixes (e.g. \rightarrow before \to).
var latexToUnicode = []struct{ from, to string }{
	{`\longrightarrow`, "⟶"}, {`\longleftarrow`, "⟵"},
	{`\leftrightarrow`, "↔"}, {`\Leftrightarrow`, "⇔"},
	{`\rightarrow`, "→"}, {`\leftarrow`, "←"},
	{`\Rightarrow`, "⇒"}, {`\Leftarrow`, "⇐"},
	{`\mapsto`, "↦"}, {`\to`, "→"},
	{`\times`, "×"}, {`\cdot`, "·"}, {`\pm`, "±"}, {`\mp`, "∓"},
	{`\leq`, "≤"}, {`\geq`, "≥"}, {`\neq`, "≠"}, {`\ne`, "≠"},
	{`\approx`, "≈"}, {`\equiv`, "≡"}, {`\sim`, "∼"}, {`\propto`, "∝"},
	{`\infty`, "∞"}, {`\partial`, "∂"}, {`\nabla`, "∇"},
	{`\sum`, "∑"}, {`\prod`, "∏"}, {`\int`, "∫"}, {`\oint`, "∮"},
	{`\sqrt`, "√"}, {`\ldots`, "…"}, {`\cdots`, "⋯"}, {`\vdots`, "⋮"},
	{`\varepsilon`, "ε"}, {`\vartheta`, "ϑ"}, {`\varphi`, "φ"}, {`\varrho`, "ϱ"},
	{`\alpha`, "α"}, {`\beta`, "β"}, {`\gamma`, "γ"}, {`\delta`, "δ"},
	{`\epsilon`, "ε"}, {`\zeta`, "ζ"}, {`\eta`, "η"}, {`\theta`, "θ"},
	{`\iota`, "ι"}, {`\kappa`, "κ"}, {`\lambda`, "λ"}, {`\mu`, "μ"}, {`\nu`, "ν"},
	{`\xi`, "ξ"}, {`\pi`, "π"}, {`\rho`, "ρ"}, {`\sigma`, "σ"},
	{`\tau`, "τ"}, {`\upsilon`, "υ"}, {`\phi`, "φ"}, {`\chi`, "χ"},
	{`\psi`, "ψ"}, {`\omega`, "ω"},
	{`\Gamma`, "Γ"}, {`\Delta`, "Δ"}, {`\Theta`, "Θ"}, {`\Lambda`, "Λ"},
	{`\Xi`, "Ξ"}, {`\Pi`, "Π"}, {`\Sigma`, "Σ"}, {`\Upsilon`, "Υ"},
	{`\Phi`, "Φ"}, {`\Psi`, "Ψ"}, {`\Omega`, "Ω"},
	{`\notin`, "∉"}, {`\subseteq`, "⊆"}, {`\supseteq`, "⊇"},
	{`\subset`, "⊂"}, {`\supset`, "⊃"}, {`\in`, "∈"},
	{`\cup`, "∪"}, {`\cap`, "∩"}, {`\emptyset`, "∅"},
	{`\forall`, "∀"}, {`\exists`, "∃"}, {`\neg`, "¬"},
	{`\land`, "∧"}, {`\lor`, "∨"}, {`\cdotp`, "·"},
	{`\hbar`, "ℏ"}, {`\ell`, "ℓ"}, {`\wp`, "℘"},
	{`\perp`, "⊥"}, {`\parallel`, "∥"}, {`\angle`, "∠"},
	{`\degree`, "°"}, {`\circ`, "∘"}, {`\bullet`, "•"},
	{`\otimes`, "⊗"}, {`\oplus`, "⊕"}, {`\ominus`, "⊖"},
	{`\implies`, "⇒"}, {`\iff`, "⇔"}, {`\because`, "∵"}, {`\therefore`, "∴"},
	{`\langle`, "⟨"}, {`\rangle`, "⟩"},
	{`\lfloor`, "⌊"}, {`\rfloor`, "⌋"}, {`\lceil`, "⌈"}, {`\rceil`, "⌉"},
	{`\dots`, "…"}, {`\mid`, "∣"}, {`\ll`, "≪"}, {`\gg`, "≫"},
	{`\wedge`, "∧"}, {`\vee`, "∨"}, {`\setminus`, "∖"},
	{`\ast`, "∗"}, {`\star`, "★"}, {`\dagger`, "†"}, {`\ddagger`, "‡"},
	{`\prime`, "′"}, {`\backslash`, `\`}, {`\Vert`, "‖"}, {`\vert`, "|"},
	{`\lnot`, "¬"},
}

var (
	mathSup = map[rune]rune{
		'0': '⁰', '1': '¹', '2': '²', '3': '³', '4': '⁴',
		'5': '⁵', '6': '⁶', '7': '⁷', '8': '⁸', '9': '⁹',
		'+': '⁺', '-': '⁻', '=': '⁼', '(': '⁽', ')': '⁾',
		'n': 'ⁿ', 'i': 'ⁱ',
	}
	mathSub = map[rune]rune{
		'0': '₀', '1': '₁', '2': '₂', '3': '₃', '4': '₄',
		'5': '₅', '6': '₆', '7': '₇', '8': '₈', '9': '₉',
		'+': '₊', '-': '₋', '=': '₌', '(': '₍', ')': '₎',
		'a': 'ₐ', 'e': 'ₑ', 'h': 'ₕ', 'i': 'ᵢ', 'j': 'ⱼ',
		'k': 'ₖ', 'l': 'ₗ', 'm': 'ₘ', 'n': 'ₙ', 'o': 'ₒ',
		'p': 'ₚ', 'r': 'ᵣ', 's': 'ₛ', 't': 'ₜ', 'u': 'ᵤ',
		'v': 'ᵥ', 'x': 'ₓ',
	}
)

func renderMathText(src string) string {
	s := strings.TrimSpace(src)
	s = strings.ReplaceAll(s, "\\,", " ")
	s = strings.ReplaceAll(s, "\\;", " ")
	s = strings.ReplaceAll(s, "\\!", "")
	s = strings.ReplaceAll(s, "\\ ", " ")
	s = strings.ReplaceAll(s, "\\quad", "  ")
	s = strings.ReplaceAll(s, "\\qquad", "    ")
	s = rewriteFrac(s)
	s = rewriteSqrt(s)
	s = rewriteBinom(s)
	s = rewriteMathEnv(s)
	s = rewriteCombining(s, `\overline`, '\u0305')
	s = rewriteCombining(s, `\vec`, '\u20d7')
	s = rewriteCombining(s, `\hat`, '\u0302')
	s = rewriteMathBB(s)
	s = unwrapMathCmd(s, `\text`)
	s = unwrapMathCmd(s, `\mathrm`)
	s = unwrapMathCmd(s, `\mathbf`)
	s = unwrapMathCmd(s, `\mathit`)
	s = unwrapMathCmd(s, `\operatorname`)
	for _, pair := range latexToUnicode {
		s = strings.ReplaceAll(s, pair.from, pair.to)
	}
	for _, cmd := range []string{`\arcsin`, `\arccos`, `\arctan`, `\sinh`, `\cosh`, `\tanh`, `\sin`, `\cos`, `\tan`, `\log`, `\ln`, `\exp`, `\lim`, `\min`, `\max`, `\det`, `\dim`, `\ker`, `\sup`, `\inf`} {
		s = strings.ReplaceAll(s, cmd, strings.TrimPrefix(cmd, `\`))
	}
	s = stripLeftRight(s)
	s = applyMathScripts(s)
	s = strings.ReplaceAll(s, "{", "")
	s = strings.ReplaceAll(s, "}", "")
	return s
}

func rewriteFrac(s string) string {
	for _, cmd := range []string{`\dfrac`, `\tfrac`, `\frac`} {
		s = replaceMathCmd(s, cmd, func(args []string) string {
			if len(args) < 2 {
				return cmd
			}
			return "(" + args[0] + ")/(" + args[1] + ")"
		}, 2)
	}
	return s
}

func rewriteSqrt(s string) string {
	return replaceMathCmd(s, `\sqrt`, func(args []string) string {
		if len(args) == 0 {
			return "√"
		}
		body := args[0]
		if len(body) <= 1 {
			return "√" + body
		}
		return "√(" + body + ")"
	}, 1)
}

func rewriteBinom(s string) string {
	return replaceMathCmd(s, `\binom`, func(args []string) string {
		if len(args) < 2 {
			return `\binom`
		}
		return "C(" + args[0] + "," + args[1] + ")"
	}, 2)
}

func rewriteMathEnv(s string) string {
	for _, env := range []string{
		"pmatrix", "bmatrix", "Bmatrix", "vmatrix", "Vmatrix",
		"matrix", "smallmatrix", "cases", "aligned", "align", "array",
	} {
		s = replaceMathEnv(s, env)
	}
	return s
}

func replaceMathEnv(s, env string) string {
	begin := `\begin{` + env + `}`
	end := `\end{` + env + `}`
	for {
		i := strings.Index(s, begin)
		if i < 0 {
			return s
		}
		rest := s[i+len(begin):]
		j := strings.Index(rest, end)
		if j < 0 {
			return s
		}
		body := rest[:j]
		s = s[:i] + formatMathEnv(env, body) + rest[j+len(end):]
	}
}

func formatMathEnv(env, body string) string {
	body = strings.TrimSpace(body)
	body = strings.ReplaceAll(body, `\\`, "\n")
	var rows [][]string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "&")
		cells := make([]string, len(parts))
		for i, p := range parts {
			cells[i] = strings.TrimSpace(p)
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return ""
	}
	left, right := "[", "]"
	switch env {
	case "pmatrix":
		left, right = "(", ")"
	case "bmatrix":
		left, right = "[", "]"
	case "Bmatrix":
		left, right = "{", "}"
	case "vmatrix":
		left, right = "|", "|"
	case "Vmatrix":
		left, right = "‖", "‖"
	case "cases":
		left, right = "{", ""
	case "aligned", "align", "array", "matrix", "smallmatrix":
		left, right = "[", "]"
	}
	if len(rows) == 1 {
		return left + strings.Join(rows[0], "  ") + right
	}
	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		open, close := left, right
		if i > 0 {
			open = strings.Repeat(" ", len([]rune(left)))
		}
		if i < len(rows)-1 {
			close = strings.Repeat(" ", len([]rune(right)))
		}
		b.WriteString(open)
		b.WriteString(strings.Join(row, "  "))
		b.WriteString(close)
	}
	return b.String()
}

var mathBB = map[rune]rune{
	'C': 'ℂ', 'H': 'ℍ', 'N': 'ℕ', 'P': 'ℙ',
	'Q': 'ℚ', 'R': 'ℝ', 'Z': 'ℤ',
}

func rewriteMathBB(s string) string {
	return replaceMathCmd(s, `\mathbb`, func(args []string) string {
		if len(args) == 0 {
			return ""
		}
		var b strings.Builder
		for _, r := range args[0] {
			if m, ok := mathBB[r]; ok {
				b.WriteRune(m)
			} else {
				b.WriteRune(r)
			}
		}
		return b.String()
	}, 1)
}

func rewriteCombining(s, cmd string, mark rune) string {
	return replaceMathCmd(s, cmd, func(args []string) string {
		if len(args) == 0 {
			return ""
		}
		var b strings.Builder
		for _, r := range args[0] {
			b.WriteRune(r)
			if !unicode.IsSpace(r) {
				b.WriteRune(mark)
			}
		}
		return b.String()
	}, 1)
}

func stripLeftRight(s string) string {
	for _, cmd := range []string{`\left`, `\right`} {
		var b strings.Builder
		b.Grow(len(s))
		for {
			i := strings.Index(s, cmd)
			if i < 0 {
				b.WriteString(s)
				break
			}
			b.WriteString(s[:i])
			rest := s[i+len(cmd):]
			if rest == "" {
				b.WriteString(cmd)
				s = ""
				break
			}
			r, size := utf8.DecodeRuneInString(rest)
			if unicode.IsLetter(r) {
				b.WriteString(cmd)
				s = rest
				continue
			}
			s = rest[size:]
			b.WriteString(rest[:size])
		}
		s = b.String()
	}
	return s
}

func unwrapMathCmd(s, cmd string) string {
	return replaceMathCmd(s, cmd, func(args []string) string {
		if len(args) == 0 {
			return ""
		}
		return args[0]
	}, 1)
}

func replaceMathCmd(s, cmd string, repl func([]string) string, argc int) string {
	if argc < 1 || !strings.Contains(s, cmd) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.Index(s, cmd)
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		rest := s[i+len(cmd):]
		args := make([]string, 0, argc)
		consumed := 0
		ok := true
		for n := 0; n < argc; n++ {
			body, nread, found := takeMathGroup(rest[consumed:])
			if !found {
				ok = false
				break
			}
			args = append(args, body)
			consumed += nread
		}
		if !ok {
			b.WriteString(cmd)
			s = rest
			continue
		}
		b.WriteString(repl(args))
		s = rest[consumed:]
	}
	return b.String()
}

func takeMathGroup(s string) (string, int, bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) {
		return "", 0, false
	}
	if s[i] == '{' {
		depth := 1
		j := i + 1
		for j < len(s) {
			switch s[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return s[i+1 : j], j + 1, true
				}
			}
			j++
		}
		return "", 0, false
	}
	if s[i] == '\\' {
		j := i + 1
		for j < len(s) {
			r, size := utf8.DecodeRuneInString(s[j:])
			if !unicode.IsLetter(r) {
				break
			}
			j += size
		}
		if j == i+1 && j < len(s) {
			_, size := utf8.DecodeRuneInString(s[j:])
			j += size
		}
		return s[i:j], j, true
	}
	_, size := utf8.DecodeRuneInString(s[i:])
	if size <= 0 {
		return "", 0, false
	}
	return s[i : i+size], i + size, true
}

func applyMathScripts(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if (s[i] == '^' || s[i] == '_') && i+1 < len(s) {
			table := mathSup
			if s[i] == '_' {
				table = mathSub
			}
			body, n := mathScriptBody(s[i+1:])
			if mapped, ok := mapMathScript(body, table); ok {
				b.WriteString(mapped)
				i += 1 + n
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func mathScriptBody(s string) (string, int) {
	if s == "" {
		return "", 0
	}
	if s[0] == '{' {
		end := strings.IndexByte(s, '}')
		if end > 0 {
			return s[1:end], end + 1
		}
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size == 1 {
		return s[:1], 1
	}
	return s[:size], size
}

func mapMathScript(body string, table map[rune]rune) (string, bool) {
	if body == "" {
		return "", false
	}
	var b strings.Builder
	for _, r := range body {
		if unicode.IsSpace(r) {
			continue
		}
		mapped, ok := table[r]
		if !ok {
			return "", false
		}
		b.WriteRune(mapped)
	}
	if b.Len() == 0 {
		return "", false
	}
	return b.String(), true
}
