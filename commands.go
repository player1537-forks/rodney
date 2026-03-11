package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// --- Inner functions (shared by CLI and serve mode) ---
// These take a browser/page/state and return (output, error).
// They never call fatal(), os.Exit(), or print to stdout/stderr.

// runOpen navigates the active page to a URL. Mutates state.ActivePage if needed.
func runOpen(browser *rod.Browser, state *State, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney open <url>")
	}
	url := args[0]
	if !strings.Contains(url, "://") {
		url = "http://" + url
	}

	pages, _ := browser.Pages()
	var page *rod.Page
	if len(pages) == 0 {
		page = browser.MustPage(url)
		state.ActivePage = 0
	} else {
		var err error
		page, err = getActivePage(browser, state)
		if err != nil {
			return "", err
		}
		if err := page.Navigate(url); err != nil {
			return "", fmt.Errorf("navigation failed: %w", err)
		}
	}
	page.MustWaitLoad()
	info, _ := page.Info()
	if info != nil {
		return info.Title, nil
	}
	return "", nil
}

func runBack(page *rod.Page) (string, error) {
	page.MustNavigateBack()
	page.MustWaitLoad()
	info, _ := page.Info()
	if info != nil {
		return info.URL, nil
	}
	return "", nil
}

func runForward(page *rod.Page) (string, error) {
	page.MustNavigateForward()
	page.MustWaitLoad()
	info, _ := page.Info()
	if info != nil {
		return info.URL, nil
	}
	return "", nil
}

func runReload(page *rod.Page, args []string) (string, error) {
	hard := false
	for _, a := range args {
		if a == "--hard" {
			hard = true
		}
	}
	if hard {
		err := (proto.PageReload{IgnoreCache: true}).Call(page)
		if err != nil {
			return "", fmt.Errorf("reload failed: %w", err)
		}
	} else {
		page.MustReload()
	}
	page.MustWaitLoad()
	return "Reloaded", nil
}

func runClearCache(page *rod.Page) (string, error) {
	err := (proto.NetworkClearBrowserCache{}).Call(page)
	if err != nil {
		return "", fmt.Errorf("clear cache failed: %w", err)
	}
	return "Browser cache cleared", nil
}

func runURL(page *rod.Page) (string, error) {
	info, err := page.Info()
	if err != nil {
		return "", fmt.Errorf("failed to get page info: %w", err)
	}
	return info.URL, nil
}

func runTitle(page *rod.Page) (string, error) {
	info, err := page.Info()
	if err != nil {
		return "", fmt.Errorf("failed to get page info: %w", err)
	}
	return info.Title, nil
}

func runHTML(page *rod.Page, args []string) (string, error) {
	if len(args) > 0 {
		el, err := page.Element(args[0])
		if err != nil {
			return "", fmt.Errorf("element not found: %w", err)
		}
		html, err := el.HTML()
		if err != nil {
			return "", fmt.Errorf("failed to get HTML: %w", err)
		}
		return html, nil
	}
	html := page.MustEval(`() => document.documentElement.outerHTML`).Str()
	return html, nil
}

func runText(page *rod.Page, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney text <selector>")
	}
	el, err := page.Element(args[0])
	if err != nil {
		return "", fmt.Errorf("element not found: %w", err)
	}
	text, err := el.Text()
	if err != nil {
		return "", fmt.Errorf("failed to get text: %w", err)
	}
	return text, nil
}

func runAttr(page *rod.Page, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: rodney attr <selector> <attribute>")
	}
	el, err := page.Element(args[0])
	if err != nil {
		return "", fmt.Errorf("element not found: %w", err)
	}
	val := el.MustAttribute(args[1])
	if val == nil {
		return "", fmt.Errorf("attribute %q not found", args[1])
	}
	return *val, nil
}

func runPDF(page *rod.Page, args []string) (string, error) {
	file := "page.pdf"
	if len(args) > 0 {
		file = args[0]
	}
	req := proto.PagePrintToPDF{}
	r, err := page.PDF(&req)
	if err != nil {
		return "", fmt.Errorf("failed to generate PDF: %w", err)
	}
	buf := make([]byte, 0)
	tmp := make([]byte, 32*1024)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	if err := os.WriteFile(file, buf, 0644); err != nil {
		return "", fmt.Errorf("failed to write PDF: %w", err)
	}
	return fmt.Sprintf("Saved %s (%d bytes)", file, len(buf)), nil
}

// formatJSResult formats a rod eval result for output.
func formatJSResult(result *proto.RuntimeRemoteObject) string {
	v := result.Value
	raw := v.JSON("", "")
	switch {
	case raw == "null" || raw == "undefined":
		return raw
	case raw == "true" || raw == "false":
		return raw
	case len(raw) > 0 && raw[0] == '"':
		return v.Str()
	case len(raw) > 0 && (raw[0] == '{' || raw[0] == '['):
		return v.JSON("", "  ")
	default:
		return raw
	}
}

func runJS(page *rod.Page, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney js <expression>")
	}
	expr := strings.Join(args, " ")
	js := fmt.Sprintf(`() => { return (%s); }`, expr)
	result, err := page.Eval(js)
	if err != nil {
		return "", fmt.Errorf("JS error: %w", err)
	}
	return formatJSResult(result), nil
}

func runClick(page *rod.Page, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney click <selector>")
	}
	el, err := page.Element(args[0])
	if err != nil {
		return "", fmt.Errorf("element not found: %w", err)
	}
	if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return "", fmt.Errorf("click failed: %w", err)
	}
	time.Sleep(100 * time.Millisecond)
	return "Clicked", nil
}

func runInput(page *rod.Page, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: rodney input <selector> <text>")
	}
	el, err := page.Element(args[0])
	if err != nil {
		return "", fmt.Errorf("element not found: %w", err)
	}
	text := strings.Join(args[1:], " ")
	el.MustSelectAllText().MustInput(text)
	return fmt.Sprintf("Typed: %s", text), nil
}

func runClear(page *rod.Page, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney clear <selector>")
	}
	el, err := page.Element(args[0])
	if err != nil {
		return "", fmt.Errorf("element not found: %w", err)
	}
	el.MustSelectAllText().MustInput("")
	return "Cleared", nil
}

func runFile(page *rod.Page, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: rodney file <selector> <path|->")
	}
	selector := args[0]
	filePath := args[1]

	el, err := page.Element(selector)
	if err != nil {
		return "", fmt.Errorf("element not found: %w", err)
	}

	if filePath == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("failed to read stdin: %w", err)
		}
		tmp, err := os.CreateTemp("", "rodney-upload-*")
		if err != nil {
			return "", fmt.Errorf("failed to create temp file: %w", err)
		}
		if _, err := tmp.Write(data); err != nil {
			tmp.Close()
			return "", fmt.Errorf("failed to write temp file: %w", err)
		}
		tmp.Close()
		filePath = tmp.Name()
	} else {
		if _, err := os.Stat(filePath); err != nil {
			return "", fmt.Errorf("file not found: %w", err)
		}
	}

	if err := el.SetFiles([]string{filePath}); err != nil {
		return "", fmt.Errorf("failed to set file: %w", err)
	}
	return fmt.Sprintf("Set file: %s", args[1]), nil
}

func runDownload(page *rod.Page, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney download <selector> [file|-]")
	}
	selector := args[0]
	outFile := ""
	if len(args) > 1 {
		outFile = args[1]
	}

	el, err := page.Element(selector)
	if err != nil {
		return "", fmt.Errorf("element not found: %w", err)
	}

	urlStr := ""
	if v := el.MustAttribute("href"); v != nil {
		urlStr = *v
	} else if v := el.MustAttribute("src"); v != nil {
		urlStr = *v
	} else {
		return "", fmt.Errorf("element has no href or src attribute")
	}

	var data []byte

	if strings.HasPrefix(urlStr, "data:") {
		data, err = decodeDataURL(urlStr)
		if err != nil {
			return "", fmt.Errorf("failed to decode data URL: %w", err)
		}
	} else {
		js := fmt.Sprintf(`async () => {
			const resp = await fetch(%q);
			if (!resp.ok) throw new Error('HTTP ' + resp.status);
			const buf = await resp.arrayBuffer();
			const bytes = new Uint8Array(buf);
			let binary = '';
			for (let i = 0; i < bytes.length; i++) {
				binary += String.fromCharCode(bytes[i]);
			}
			return btoa(binary);
		}`, urlStr)
		result, err := page.Eval(js)
		if err != nil {
			return "", fmt.Errorf("download failed: %w", err)
		}
		data, err = base64.StdEncoding.DecodeString(result.Value.Str())
		if err != nil {
			return "", fmt.Errorf("failed to decode response: %w", err)
		}
	}

	if outFile == "-" {
		os.Stdout.Write(data)
		return "", nil
	}

	if outFile == "" {
		outFile = inferDownloadFilename(urlStr)
	}

	if err := os.WriteFile(outFile, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}
	return fmt.Sprintf("Saved %s (%d bytes)", outFile, len(data)), nil
}

func runSelect(page *rod.Page, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: rodney select <selector> <value>")
	}
	js := fmt.Sprintf(`() => {
		const el = document.querySelector(%q);
		if (!el) throw new Error('element not found');
		el.value = %q;
		el.dispatchEvent(new Event('change', {bubbles: true}));
		return el.value;
	}`, args[0], args[1])
	result, err := page.Eval(js)
	if err != nil {
		return "", fmt.Errorf("select failed: %w", err)
	}
	return fmt.Sprintf("Selected: %s", result.Value.Str()), nil
}

func runSubmit(page *rod.Page, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney submit <selector>")
	}
	_, err := page.Element(args[0])
	if err != nil {
		return "", fmt.Errorf("form not found: %w", err)
	}
	page.MustEval(fmt.Sprintf(`() => document.querySelector(%q).submit()`, args[0]))
	return "Submitted", nil
}

func runHover(page *rod.Page, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney hover <selector>")
	}
	el, err := page.Element(args[0])
	if err != nil {
		return "", fmt.Errorf("element not found: %w", err)
	}
	el.MustHover()
	return "Hovered", nil
}

func runFocus(page *rod.Page, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney focus <selector>")
	}
	el, err := page.Element(args[0])
	if err != nil {
		return "", fmt.Errorf("element not found: %w", err)
	}
	el.MustFocus()
	return "Focused", nil
}

func runWait(page *rod.Page, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney wait <selector>")
	}
	el, err := page.Element(args[0])
	if err != nil {
		return "", fmt.Errorf("element not found: %w", err)
	}
	el.MustWaitVisible()
	return "Element visible", nil
}

func runWaitLoad(page *rod.Page) (string, error) {
	page.MustWaitLoad()
	return "Page loaded", nil
}

func runWaitStable(page *rod.Page) (string, error) {
	page.MustWaitStable()
	return "DOM stable", nil
}

func runWaitIdle(page *rod.Page) (string, error) {
	page.MustWaitIdle()
	return "Network idle", nil
}

func runSleep(args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney sleep <seconds>")
	}
	secs, err := strconv.ParseFloat(args[0], 64)
	if err != nil {
		return "", fmt.Errorf("invalid seconds: %w", err)
	}
	time.Sleep(time.Duration(secs * float64(time.Second)))
	return "", nil
}

func runScreenshot(page *rod.Page, args []string) (string, error) {
	var file string
	width := 1280
	height := 0
	fullPage := true

	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-w", "--width":
			i++
			if i >= len(args) {
				return "", fmt.Errorf("missing value for %s", args[i-1])
			}
			v, err := strconv.Atoi(args[i])
			if err != nil {
				return "", fmt.Errorf("invalid width: %w", err)
			}
			width = v
		case "-h", "--height":
			i++
			if i >= len(args) {
				return "", fmt.Errorf("missing value for %s", args[i-1])
			}
			v, err := strconv.Atoi(args[i])
			if err != nil {
				return "", fmt.Errorf("invalid height: %w", err)
			}
			height = v
			fullPage = false
		default:
			positional = append(positional, args[i])
		}
	}

	if len(positional) > 0 {
		file = positional[0]
	} else {
		file = nextAvailableFile("screenshot", ".png")
	}

	viewportHeight := height
	if viewportHeight == 0 {
		viewportHeight = 720
	}
	err := proto.EmulationSetDeviceMetricsOverride{
		Width:             width,
		Height:            viewportHeight,
		DeviceScaleFactor: 1,
	}.Call(page)
	if err != nil {
		return "", fmt.Errorf("failed to set viewport: %w", err)
	}

	data, err := page.Screenshot(fullPage, nil)
	if err != nil {
		return "", fmt.Errorf("screenshot failed: %w", err)
	}
	if err := os.WriteFile(file, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write screenshot: %w", err)
	}
	return file, nil
}

func runScreenshotEl(page *rod.Page, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney screenshot-el <selector> [file]")
	}
	file := "element.png"
	if len(args) > 1 {
		file = args[1]
	}
	el, err := page.Element(args[0])
	if err != nil {
		return "", fmt.Errorf("element not found: %w", err)
	}
	data, err := el.Screenshot(proto.PageCaptureScreenshotFormatPng, 0)
	if err != nil {
		return "", fmt.Errorf("screenshot failed: %w", err)
	}
	if err := os.WriteFile(file, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write screenshot: %w", err)
	}
	return fmt.Sprintf("Saved %s (%d bytes)", file, len(data)), nil
}

func runPages(browser *rod.Browser, state *State) (string, error) {
	pages, err := browser.Pages()
	if err != nil {
		return "", fmt.Errorf("failed to list pages: %w", err)
	}
	var sb strings.Builder
	for i, p := range pages {
		marker := " "
		if i == state.ActivePage {
			marker = "*"
		}
		info, _ := p.Info()
		if info != nil {
			sb.WriteString(fmt.Sprintf("%s [%d] %s - %s\n", marker, i, info.Title, info.URL))
		} else {
			sb.WriteString(fmt.Sprintf("%s [%d] (unknown)\n", marker, i))
		}
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func runPage(browser *rod.Browser, state *State, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney page <index>")
	}
	idx, err := strconv.Atoi(args[0])
	if err != nil {
		return "", fmt.Errorf("invalid index: %w", err)
	}
	pages, err := browser.Pages()
	if err != nil {
		return "", fmt.Errorf("failed to list pages: %w", err)
	}
	if idx < 0 || idx >= len(pages) {
		return "", fmt.Errorf("page index %d out of range (0-%d)", idx, len(pages)-1)
	}
	state.ActivePage = idx
	info, _ := pages[idx].Info()
	if info != nil {
		return fmt.Sprintf("Switched to [%d] %s - %s", idx, info.Title, info.URL), nil
	}
	return "", nil
}

func runNewPage(browser *rod.Browser, state *State, args []string) (string, error) {
	url := ""
	if len(args) > 0 {
		url = args[0]
		if !strings.Contains(url, "://") {
			url = "http://" + url
		}
	}

	var page *rod.Page
	if url != "" {
		page = browser.MustPage(url)
		page.MustWaitLoad()
	} else {
		page = browser.MustPage("")
	}

	pages, _ := browser.Pages()
	for i, p := range pages {
		if p.TargetID == page.TargetID {
			state.ActivePage = i
			break
		}
	}

	info, _ := page.Info()
	if info != nil {
		return fmt.Sprintf("Opened [%d] %s", state.ActivePage, info.URL), nil
	}
	return "", nil
}

func runClosePage(browser *rod.Browser, state *State, args []string) (string, error) {
	pages, err := browser.Pages()
	if err != nil {
		return "", fmt.Errorf("failed to list pages: %w", err)
	}
	if len(pages) <= 1 {
		return "", fmt.Errorf("cannot close the last page")
	}

	idx := state.ActivePage
	if len(args) > 0 {
		idx, err = strconv.Atoi(args[0])
		if err != nil {
			return "", fmt.Errorf("invalid index: %w", err)
		}
	}
	if idx < 0 || idx >= len(pages) {
		return "", fmt.Errorf("page index %d out of range", idx)
	}

	pages[idx].MustClose()

	if state.ActivePage >= len(pages)-1 {
		state.ActivePage = len(pages) - 2
	}
	if state.ActivePage < 0 {
		state.ActivePage = 0
	}
	return fmt.Sprintf("Closed page %d", idx), nil
}

// runExists returns (output, exitCode, error). exitCode 0=true, 1=false.
func runExists(page *rod.Page, args []string) (string, int, error) {
	if len(args) < 1 {
		return "", 2, fmt.Errorf("usage: rodney exists <selector>")
	}
	has, _, err := page.Has(args[0])
	if err != nil {
		return "", 2, fmt.Errorf("query failed: %w", err)
	}
	if has {
		return "true", 0, nil
	}
	return "false", 1, nil
}

func runCount(page *rod.Page, args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("usage: rodney count <selector>")
	}
	els, err := page.Elements(args[0])
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}
	return fmt.Sprintf("%d", len(els)), nil
}

// runVisible returns (output, exitCode, error). exitCode 0=visible, 1=not visible.
func runVisible(page *rod.Page, args []string) (string, int, error) {
	if len(args) < 1 {
		return "", 2, fmt.Errorf("usage: rodney visible <selector>")
	}
	el, err := page.Element(args[0])
	if err != nil {
		return "false", 1, nil
	}
	visible, err := el.Visible()
	if err != nil {
		return "false", 1, nil
	}
	if visible {
		return "true", 0, nil
	}
	return "false", 1, nil
}

// runAssert returns (output, exitCode, error). exitCode 0=pass, 1=fail.
func runAssert(page *rod.Page, args []string) (string, int, error) {
	if len(args) < 1 {
		return "", 2, fmt.Errorf("usage: rodney assert <js-expression> [expected] [--message msg]")
	}

	expr, expected, message := parseAssertArgs(args)
	if expr == "" {
		return "", 2, fmt.Errorf("usage: rodney assert <js-expression> [expected] [--message msg]")
	}

	js := fmt.Sprintf(`() => { return (%s); }`, expr)
	result, err := page.Eval(js)
	if err != nil {
		return "", 2, fmt.Errorf("JS error: %w", err)
	}

	v := result.Value
	raw := v.JSON("", "")
	var actual string
	switch {
	case raw == "null" || raw == "undefined":
		actual = raw
	case raw == "true" || raw == "false":
		actual = raw
	case len(raw) > 0 && raw[0] == '"':
		actual = v.Str()
	case len(raw) > 0 && (raw[0] == '{' || raw[0] == '['):
		actual = v.JSON("", "  ")
	default:
		actual = raw
	}

	if expected != nil {
		if actual == *expected {
			return "pass", 0, nil
		}
		return formatAssertFail(actual, expected, message), 1, nil
	}

	switch raw {
	case "false", "0", "null", "undefined", `""`:
		return formatAssertFail(actual, nil, message), 1, nil
	default:
		return "pass", 0, nil
	}
}

// runAXTree runs the ax-tree command.
func runAXTree(page *rod.Page, args []string) (string, error) {
	var depth *int
	jsonOutput := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--depth":
			i++
			if i >= len(args) {
				return "", fmt.Errorf("missing value for --depth")
			}
			v, err := strconv.Atoi(args[i])
			if err != nil {
				return "", fmt.Errorf("invalid depth: %w", err)
			}
			depth = &v
		case "--json":
			jsonOutput = true
		default:
			return "", fmt.Errorf("unknown flag: %s\nusage: rodney ax-tree [--depth N] [--json]", args[i])
		}
	}

	result, err := proto.AccessibilityGetFullAXTree{Depth: depth}.Call(page)
	if err != nil {
		return "", fmt.Errorf("failed to get accessibility tree: %w", err)
	}

	if jsonOutput {
		return formatAXTreeJSON(result.Nodes), nil
	}
	return strings.TrimRight(formatAXTree(result.Nodes), "\n"), nil
}

// runAXFind runs the ax-find command. Returns (output, exitCode, error).
func runAXFind(page *rod.Page, args []string) (string, int, error) {
	var name, role string
	jsonOutput := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			i++
			if i >= len(args) {
				return "", 2, fmt.Errorf("missing value for --name")
			}
			name = args[i]
		case "--role":
			i++
			if i >= len(args) {
				return "", 2, fmt.Errorf("missing value for --role")
			}
			role = args[i]
		case "--json":
			jsonOutput = true
		default:
			return "", 2, fmt.Errorf("unknown flag: %s\nusage: rodney ax-find [--name N] [--role R] [--json]", args[i])
		}
	}

	nodes, err := queryAXNodes(page, name, role)
	if err != nil {
		return "", 2, fmt.Errorf("query failed: %w", err)
	}

	if len(nodes) == 0 {
		return "No matching nodes", 1, nil
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(nodes, "", "  ")
		return string(data), 0, nil
	}
	return strings.TrimRight(formatAXNodeList(nodes), "\n"), 0, nil
}

// runAXNode runs the ax-node command.
func runAXNode(page *rod.Page, args []string) (string, error) {
	jsonOutput := false
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		default:
			positional = append(positional, args[i])
		}
	}

	if len(positional) < 1 {
		return "", fmt.Errorf("usage: rodney ax-node <selector> [--json]")
	}
	selector := positional[0]

	node, err := getAXNode(page, selector)
	if err != nil {
		return "", err
	}

	if jsonOutput {
		return formatAXNodeDetailJSON(node), nil
	}
	return strings.TrimRight(formatAXNodeDetail(node), "\n"), nil
}

// --- CLI wrappers (thin: call runXxx, handle fatal/stdout/exit) ---

func cmdStart(args []string) {
	ignoreCertErrors := false
	headless := true
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--insecure", "-k":
			ignoreCertErrors = true
		case "--show":
			headless = false
		default:
			fatal("unknown flag: %s\nusage: rodney start [--show] [--insecure]", args[i])
		}
	}

	// Check if already running
	if s, err := loadState(); err == nil {
		if b, err := connectBrowser(s); err == nil {
			b.MustClose()
			removeState()
		}
	}

	launchChrome(headless, ignoreCertErrors, true)
}

func cmdConnect(args []string) {
	if len(args) < 1 {
		fatal("usage: rodney connect <host:port>")
	}
	hostport := args[0]
	if !strings.Contains(hostport, "://") {
		hostport = "ws://" + hostport
	}
	browser := rod.New().ControlURL(hostport)
	if err := browser.Connect(); err != nil {
		fatal("failed to connect: %v", err)
	}
	pages, _ := browser.Pages()
	state := &State{
		DebugURL:   hostport,
		ChromePID:  0,
		ActivePage: 0,
	}
	if err := saveState(state); err != nil {
		fatal("failed to save state: %v", err)
	}
	fmt.Printf("Connected to %s (%d pages)\n", hostport, len(pages))
}

func cmdStop(args []string) {
	s, err := loadState()
	if err != nil {
		fatal("%v", err)
	}
	browser, err := connectBrowser(s)
	if err != nil {
		if s.ChromePID > 0 {
			proc, err := os.FindProcess(s.ChromePID)
			if err == nil {
				proc.Signal(syscall.SIGTERM)
			}
		}
	} else if s.ChromePID > 0 {
		browser.MustClose()
	}
	if s.ProxyPID > 0 {
		if proc, err := os.FindProcess(s.ProxyPID); err == nil {
			proc.Signal(syscall.SIGTERM)
		}
	}
	removeState()
	fmt.Println("Chrome stopped")
}

func cmdStatus(args []string) {
	s, err := loadState()
	if err != nil {
		fmt.Println("No active browser session")
		return
	}
	browser, err := connectBrowser(s)
	if err != nil {
		fmt.Printf("Browser not responding (PID %d, state may be stale)\n", s.ChromePID)
		return
	}
	pages, _ := browser.Pages()
	fmt.Printf("Browser running (PID %d)\n", s.ChromePID)
	fmt.Printf("Debug URL: %s\n", s.DebugURL)
	fmt.Printf("Pages: %d\n", len(pages))
	fmt.Printf("Active page: %d\n", s.ActivePage)
	if page, err := getActivePage(browser, s); err == nil {
		info, _ := page.Info()
		if info != nil {
			fmt.Printf("Current: %s - %s\n", info.Title, info.URL)
		}
	}
}

func cmdOpen(args []string) {
	s, err := loadState()
	if err != nil {
		fatal("%v", err)
	}
	browser, err := connectBrowser(s)
	if err != nil {
		fatal("%v", err)
	}
	result, err := runOpen(browser, s, args)
	if err != nil {
		fatal("%v", err)
	}
	saveState(s)
	if result != "" {
		fmt.Println(result)
	}
}

func cmdBack(args []string) {
	_, _, page := withPage()
	result, err := runBack(page)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdForward(args []string) {
	_, _, page := withPage()
	result, err := runForward(page)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdReload(args []string) {
	_, _, page := withPage()
	result, err := runReload(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdClearCache(args []string) {
	_, _, page := withPage()
	result, err := runClearCache(page)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdURL(args []string) {
	_, _, page := withPage()
	result, err := runURL(page)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdTitle(args []string) {
	_, _, page := withPage()
	result, err := runTitle(page)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdHTML(args []string) {
	_, _, page := withPage()
	result, err := runHTML(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdText(args []string) {
	_, _, page := withPage()
	result, err := runText(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdAttr(args []string) {
	_, _, page := withPage()
	result, err := runAttr(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdPDF(args []string) {
	_, _, page := withPage()
	result, err := runPDF(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdJS(args []string) {
	_, _, page := withPage()
	result, err := runJS(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdClick(args []string) {
	_, _, page := withPage()
	result, err := runClick(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdInput(args []string) {
	_, _, page := withPage()
	result, err := runInput(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdClear(args []string) {
	_, _, page := withPage()
	result, err := runClear(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdFile(args []string) {
	_, _, page := withPage()
	result, err := runFile(page, args)
	if err != nil {
		fatal("%v", err)
	}
	if result != "" {
		fmt.Println(result)
	}
}

func cmdDownload(args []string) {
	_, _, page := withPage()
	result, err := runDownload(page, args)
	if err != nil {
		fatal("%v", err)
	}
	if result != "" {
		fmt.Println(result)
	}
}

func cmdSelect(args []string) {
	_, _, page := withPage()
	result, err := runSelect(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdSubmit(args []string) {
	_, _, page := withPage()
	result, err := runSubmit(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdHover(args []string) {
	_, _, page := withPage()
	result, err := runHover(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdFocus(args []string) {
	_, _, page := withPage()
	result, err := runFocus(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdWait(args []string) {
	_, _, page := withPage()
	result, err := runWait(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdWaitLoad(args []string) {
	_, _, page := withPage()
	result, err := runWaitLoad(page)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdWaitStable(args []string) {
	_, _, page := withPage()
	result, err := runWaitStable(page)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdWaitIdle(args []string) {
	_, _, page := withPage()
	result, err := runWaitIdle(page)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdSleep(args []string) {
	_, err := runSleep(args)
	if err != nil {
		fatal("%v", err)
	}
}

func cmdScreenshot(args []string) {
	_, _, page := withPage()
	result, err := runScreenshot(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdScreenshotEl(args []string) {
	_, _, page := withPage()
	result, err := runScreenshotEl(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdPages(args []string) {
	s, err := loadState()
	if err != nil {
		fatal("%v", err)
	}
	browser, err := connectBrowser(s)
	if err != nil {
		fatal("%v", err)
	}
	result, err := runPages(browser, s)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdPage(args []string) {
	s, err := loadState()
	if err != nil {
		fatal("%v", err)
	}
	browser, err := connectBrowser(s)
	if err != nil {
		fatal("%v", err)
	}
	result, err := runPage(browser, s, args)
	if err != nil {
		fatal("%v", err)
	}
	saveState(s)
	fmt.Println(result)
}

func cmdNewPage(args []string) {
	s, err := loadState()
	if err != nil {
		fatal("%v", err)
	}
	browser, err := connectBrowser(s)
	if err != nil {
		fatal("%v", err)
	}
	result, err := runNewPage(browser, s, args)
	if err != nil {
		fatal("%v", err)
	}
	saveState(s)
	if result != "" {
		fmt.Println(result)
	}
}

func cmdClosePage(args []string) {
	s, err := loadState()
	if err != nil {
		fatal("%v", err)
	}
	browser, err := connectBrowser(s)
	if err != nil {
		fatal("%v", err)
	}
	result, err := runClosePage(browser, s, args)
	if err != nil {
		fatal("%v", err)
	}
	saveState(s)
	fmt.Println(result)
}

func cmdExists(args []string) {
	_, _, page := withPage()
	result, exitCode, err := runExists(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
	os.Exit(exitCode)
}

func cmdCount(args []string) {
	_, _, page := withPage()
	result, err := runCount(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdVisible(args []string) {
	_, _, page := withPage()
	result, exitCode, err := runVisible(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
	os.Exit(exitCode)
}

func cmdAssert(args []string) {
	_, _, page := withPage()
	result, exitCode, err := runAssert(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
	os.Exit(exitCode)
}

func cmdAXTree(args []string) {
	_, _, page := withPage()
	result, err := runAXTree(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}

func cmdAXFind(args []string) {
	_, _, page := withPage()
	result, exitCode, err := runAXFind(page, args)
	if err != nil {
		fatal("%v", err)
	}
	if exitCode == 1 {
		fmt.Fprintln(os.Stderr, result)
		os.Exit(1)
	}
	fmt.Println(result)
}

func cmdAXNode(args []string) {
	_, _, page := withPage()
	result, err := runAXNode(page, args)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(result)
}
