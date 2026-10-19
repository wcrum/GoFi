package main

// The full-screen view of `gofictl top`, in the spirit of k9s and lazygit:
// an information header with key hints, a bordered flows panel with a
// selectable row, phase colours and a per-flow throughput trend, and a
// describe panel for the selected flow. Everything is plain ANSI; no
// terminal library.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/internal/testkit/gofiapi"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	fgOrange   = "\x1b[38;5;214m"
	fgAqua     = "\x1b[38;5;80m"
	fgBlue     = "\x1b[38;5;75m"
	fgDim      = "\x1b[38;5;245m"
	fgGreen    = "\x1b[38;5;114m"
	fgYellow   = "\x1b[38;5;221m"
	fgRed      = "\x1b[38;5;203m"
	fgWhite    = "\x1b[38;5;255m"
	bgSelected = "\x1b[48;5;238m"
	trendChars = "▁▂▃▄▅▆▇█"
	trendLen   = 16
)

type tui struct {
	client *gofiapi.Client
	server string
	mode   string
	color  bool

	prev, cur snapshot
	trend     map[string][]float64

	selected    string
	describe    bool
	detail      []string
	detailFor   string
	scroll      int
	notice      string
	noticeError bool
	noticeUntil time.Time
}

// runTUI owns the terminal until q, Esc, Ctrl-C, or ctx ends.
func runTUI(ctx context.Context, client *gofiapi.Client, server, mode string, interval time.Duration) error {
	t := &tui{client: client, server: server, mode: mode, color: wantColor(), trend: map[string][]float64{}}
	keys := make(chan string, 8)
	var restore func()
	if isTerminal(os.Stdin) {
		restore = rawInput(os.Stdin)
		go readKeys(os.Stdin, restore != nil, keys)
	}
	out := bufio.NewWriter(os.Stdout)
	fmt.Fprint(out, "\x1b[?1049h\x1b[?25l")
	out.Flush()
	defer func() {
		fmt.Fprint(out, ansiReset+"\x1b[?25h\x1b[?1049l")
		out.Flush()
		if restore != nil {
			restore()
		}
	}()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	t.tick(ctx)
	for {
		fmt.Fprint(out, t.render())
		out.Flush()
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			t.tick(ctx)
		case key := <-keys:
			if quit := t.handle(ctx, key); quit {
				return nil
			}
		}
	}
}

func wantColor() bool {
	if _, off := os.LookupEnv("NO_COLOR"); off {
		return false
	}
	return os.Getenv("TERM") != "dumb"
}

// tick refreshes the snapshot, trends, selection, and the describe panel.
func (t *tui) tick(ctx context.Context) {
	cur := collect(ctx, t.client)
	if cur.err != nil {
		t.cur.err = cur.err
		t.cur.at = cur.at
		return
	}
	t.prev, t.cur = t.cur, cur
	list, _, _ := rows(t.prev, t.cur)
	seen := map[string]bool{}
	for _, row := range list {
		seen[row.name] = true
		h := append(t.trend[row.name], row.r.frames)
		if len(h) > trendLen {
			h = h[len(h)-trendLen:]
		}
		t.trend[row.name] = h
	}
	for name := range t.trend {
		if !seen[name] {
			delete(t.trend, name)
		}
	}
	if !seen[t.selected] {
		t.selected = ""
		if len(list) > 0 {
			t.selected = list[0].name
		}
	}
	if t.describe {
		t.loadDetail(ctx)
	}
}

func (t *tui) loadDetail(ctx context.Context) {
	if t.selected == "" {
		t.detail, t.detailFor = nil, ""
		return
	}
	def, err := t.client.Get(ctx, t.selected)
	if err != nil {
		t.detail = []string{"error: " + describe(err)}
		t.detailFor = t.selected
		return
	}
	var lines []string
	if status, err := t.client.Status(ctx, t.selected); err == nil {
		started := "-"
		if status.StartedAt != nil {
			started = age(status.StartedAt.AsTime()) + " ago"
		}
		lines = append(lines, fmt.Sprintf("phase: %s   state: %s   restarts: %d   runtime: %s   started: %s", t.paint(phaseColor(status.Phase), dash(status.Phase)), dash(status.Desired), status.Restarts, dash(status.RuntimeStatus), started))
		if status.Message != "" {
			lines = append(lines, t.paint(fgYellow, "message: "+status.Message))
		}
		if status.Problems > 0 {
			lines = append(lines, t.paint(fgRed, fmt.Sprintf("problems: %d (%d errors, %d warnings)", status.Problems, status.Errors, status.Warnings)))
		}
		lines = append(lines, "")
	}
	data, err := desired.EncodeFlow(def)
	if err != nil {
		lines = append(lines, "error: "+err.Error())
	} else {
		lines = append(lines, strings.Split(strings.TrimRight(string(data), "\n"), "\n")...)
	}
	if t.detailFor != t.selected {
		t.scroll = 0
	}
	t.detail, t.detailFor = lines, t.selected
}

// handle applies one key; true means quit.
func (t *tui) handle(ctx context.Context, key string) bool {
	list, _, _ := rows(t.prev, t.cur)
	index := -1
	for i, row := range list {
		if row.name == t.selected {
			index = i
		}
	}
	move := func(to int) {
		if len(list) == 0 {
			return
		}
		if to < 0 {
			to = 0
		}
		if to >= len(list) {
			to = len(list) - 1
		}
		t.selected = list[to].name
		if t.describe {
			t.loadDetail(ctx)
		}
	}
	switch key {
	case "q", "ctrl-c":
		return true
	case "esc":
		if t.describe {
			t.describe = false
			return false
		}
		return true
	case "down", "j":
		move(index + 1)
	case "up", "k":
		move(index - 1)
	case "g", "home":
		move(0)
	case "G", "end":
		move(len(list) - 1)
	case "enter", "d":
		t.describe = !t.describe
		if t.describe {
			t.loadDetail(ctx)
		}
	case "pgdn", "ctrl-d":
		t.scroll += 10
	case "pgup", "ctrl-u":
		t.scroll -= 10
		if t.scroll < 0 {
			t.scroll = 0
		}
	case "s":
		if index < 0 {
			return false
		}
		stopped := false
		for _, status := range t.cur.statuses {
			if status.Name == t.selected && status.Desired == "stopped" {
				stopped = true
			}
		}
		if stopped {
			t.act("started "+t.selected, t.client.Start(ctx, t.selected))
		} else {
			t.act("stopped "+t.selected, t.client.Stop(ctx, t.selected))
		}
		t.tick(ctx)
	case "r":
		if index < 0 {
			return false
		}
		t.act("restarted "+t.selected, t.client.Restart(ctx, t.selected))
		t.tick(ctx)
	}
	return false
}

func (t *tui) act(done string, err error) {
	t.noticeUntil = time.Now().Add(4 * time.Second)
	if err != nil {
		t.notice, t.noticeError = describe(err), true
		return
	}
	t.notice, t.noticeError = done, false
}

func (t *tui) paint(code, s string) string {
	if !t.color || code == "" {
		return s
	}
	return code + s + ansiReset
}

func phaseColor(phase string) string {
	switch phase {
	case "running":
		return fgGreen
	case "starting", "stopping":
		return fgYellow
	case "crashed", "failed", "invalid":
		return fgRed
	case "stopped":
		return fgDim
	}
	return fgWhite
}

// render draws the whole screen for the current state.
func (t *tui) render() string {
	width, height := terminalSize(os.Stdout)
	if width < 40 {
		width = 120
	}
	if height < 8 {
		height = 40
	}
	list, total, running := rows(t.prev, t.cur)

	var lines []string
	// Header: information on the left, key hints on the right.
	info := []string{
		t.paint(fgOrange+ansiBold, "GoFi top"),
		t.paint(fgOrange, "Server:  ") + t.server,
		t.paint(fgOrange, "Mode:    ") + dash(t.mode),
		t.paint(fgOrange, "Flows:   ") + fmt.Sprintf("%d (%d running)", len(list), running),
		t.paint(fgOrange, "Rates:   ") + fmt.Sprintf("%s frames/s  %s  %s sinks/s  %s rejected/s", rate(total.r.frames), bytesRate(total.r.bytes), rate(total.r.sinkOK), rate(total.r.rejected)),
		t.paint(fgOrange, "Process: ") + fmt.Sprintf("RSS %s  goroutines %.0f", humanBytes(t.cur.rss), t.cur.goroutines),
	}
	hints := [][2]string{{"↑/↓ j/k", "select"}, {"enter", "describe"}, {"s", "stop/start"}, {"r", "restart"}, {"g/G", "first/last"}, {"q", "quit"}}
	var hintLines []string
	for i := 0; i+1 < len(hints); i += 2 {
		hintLines = append(hintLines, fmt.Sprintf("%s %s   %s %s", t.paint(fgBlue, "<"+hints[i][0]+">"), t.paint(fgDim, hints[i][1]), t.paint(fgBlue, "<"+hints[i+1][0]+">"), t.paint(fgDim, hints[i+1][1])))
	}
	hintWidth := 0
	for _, h := range hintLines {
		if visibleLen(h) > hintWidth {
			hintWidth = visibleLen(h)
		}
	}
	for i, line := range info {
		if i > 0 && i-1 < len(hintLines) && width-visibleLen(line) > hintWidth+4 {
			pad := width - visibleLen(line) - hintWidth
			line += strings.Repeat(" ", pad) + hintLines[i-1]
		}
		lines = append(lines, line)
	}
	if t.cur.err != nil {
		lines = append(lines, t.paint(fgRed, "controller unavailable: "+t.cur.err.Error()))
	} else if t.notice != "" && time.Now().Before(t.noticeUntil) {
		color := fgGreen
		if t.noticeError {
			color = fgRed
		}
		lines = append(lines, t.paint(color, t.notice))
	} else {
		lines = append(lines, t.paint(fgDim, t.cur.at.Format("15:04:05")))
	}

	// Panels fill what is left: flows, and the describe panel when open.
	remaining := height - len(lines)
	flowsHeight := remaining
	describeHeight := 0
	if t.describe {
		flowsHeight = len(list) + 3 // border, header, rows
		if flowsHeight > remaining/2 {
			flowsHeight = remaining / 2
		}
		if flowsHeight < 4 {
			flowsHeight = 4
		}
		describeHeight = remaining - flowsHeight
	}
	lines = append(lines, t.flowsPanel(list, total, width, flowsHeight)...)
	if t.describe && describeHeight >= 3 {
		lines = append(lines, t.describePanel(width, describeHeight)...)
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	var b strings.Builder
	b.WriteString("\x1b[H")
	for i, line := range lines {
		b.WriteString(clip(line, width))
		b.WriteString("\x1b[K")
		if i < len(lines)-1 {
			b.WriteString("\r\n")
		}
	}
	b.WriteString("\x1b[J")
	return b.String()
}

// flowsPanel is the bordered table with the selected row highlighted.
func (t *tui) flowsPanel(list []flowRow, total flowRow, width, height int) []string {
	inner := width - 4
	header := []string{"NAME", "PHASE", "FRAMES/s", "BYTES/s", "SINK OK/s", "SINK ERR/s", "REJECT/s", "FRAMES", "BYTES", "TREND"}
	right := []bool{false, false, true, true, true, true, true, true, true, false}
	plain := make([][]string, 0, len(list)+1)
	for _, row := range list {
		plain = append(plain, append(cells(row)[:9], t.sparkline(row.name)))
	}
	if len(list) > 1 {
		plain = append(plain, append(cells(total)[:9], ""))
	}
	// Narrow terminals lose the least useful columns first, never TREND.
	for _, name := range []string{"BYTES", "FRAMES", "SINK ERR/s", "REJECT/s", "SINK OK/s"} {
		drop := indexOfString(header, name)
		if tableWidth(columnWidths(header, plain)) <= inner || drop < 0 {
			break
		}
		header = removeColumn(header, drop)
		right = removeBool(right, drop)
		for i := range plain {
			plain[i] = removeColumn(plain[i], drop)
		}
	}
	trendCol := len(header) - 1
	widths := columnWidths(header, plain)
	var body []string
	body = append(body, t.paint(ansiBold+fgWhite, joinCells(header, widths, right, nil)))
	bodyRows := height - 3 // top border, header, bottom border
	if bodyRows < 1 {
		bodyRows = 1
	}
	start := 0
	if selected := indexOf(list, t.selected); selected >= bodyRows {
		start = selected - bodyRows + 1
	}
	for i := start; i < len(plain) && len(body) < bodyRows+1; i++ {
		row := plain[i]
		isTotal := len(list) > 1 && i == len(plain)-1
		if isTotal && len(body) < bodyRows {
			body = append(body, t.paint(fgDim, joinCells(row, widths, right, nil)))
			continue
		}
		if isTotal {
			break
		}
		phase := list[i].phase
		line := joinCells(row, widths, right, func(col int, cell string) string {
			switch col {
			case 1:
				return t.paint(phaseColor(phase), cell)
			case trendCol:
				return t.paint(fgAqua, cell)
			}
			return cell
		})
		if list[i].name == t.selected {
			pad := inner - visibleLen(line)
			if pad > 0 {
				line += strings.Repeat(" ", pad)
			}
			line = t.paint(bgSelected+ansiBold, line)
		}
		body = append(body, line)
	}
	if len(list) == 0 {
		body = append(body, t.paint(fgDim, "No flows configured. Apply one with: gofictl apply -f flows.yaml"))
	}
	return t.box(fmt.Sprintf("flows(%d)", len(list)), body, width, height, true)
}

func (t *tui) describePanel(width, height int) []string {
	lines := t.detail
	if t.scroll > len(lines)-1 && len(lines) > 0 {
		t.scroll = len(lines) - 1
	}
	if t.scroll > 0 && t.scroll < len(lines) {
		lines = lines[t.scroll:]
	}
	title := "describe"
	if t.detailFor != "" {
		title = "describe(" + t.detailFor + ")"
	}
	return t.box(title, lines, width, height, false)
}

// box draws a rounded border around body, cut or padded to height lines.
func (t *tui) box(title string, body []string, width, height int, focused bool) []string {
	// One cell of padding inside each border.
	inner := width - 4
	border := fgDim
	if focused {
		border = fgAqua
	}
	top := "╭─ " + title + " "
	fill := width - visibleLen(top) - 1
	if fill < 0 {
		fill = 0
	}
	out := []string{t.paint(border, top+strings.Repeat("─", fill)+"╮")}
	rowsAvail := height - 2
	for i := 0; i < rowsAvail; i++ {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		if i == rowsAvail-1 && len(body) > rowsAvail {
			line = t.paint(fgDim, fmt.Sprintf("… %d more", len(body)-rowsAvail+1))
		}
		line = clip(line, inner)
		pad := inner - visibleLen(line)
		if pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		out = append(out, t.paint(border, "│")+" "+line+" "+t.paint(border, "│"))
	}
	out = append(out, t.paint(border, "╰"+strings.Repeat("─", width-2)+"╯"))
	return out
}

// sparkline draws the recent frames/s history of one flow.
func (t *tui) sparkline(name string) string {
	h := t.trend[name]
	if len(h) == 0 {
		return ""
	}
	max := 0.0
	for _, v := range h {
		if v > max {
			max = v
		}
	}
	runes := []rune(trendChars)
	var b strings.Builder
	for _, v := range h {
		idx := 0
		if max > 0 {
			idx = int(v / max * float64(len(runes)-1))
		}
		b.WriteRune(runes[idx])
	}
	return b.String()
}

func indexOfString(list []string, want string) int {
	for i, s := range list {
		if s == want {
			return i
		}
	}
	return -1
}

func tableWidth(widths []int) int {
	total := 0
	for _, w := range widths {
		total += w + 2
	}
	return total - 2
}

func removeColumn(cells []string, i int) []string {
	if i >= len(cells) {
		return cells
	}
	out := make([]string, 0, len(cells)-1)
	out = append(out, cells[:i]...)
	return append(out, cells[i+1:]...)
}

func removeBool(cells []bool, i int) []bool {
	if i >= len(cells) {
		return cells
	}
	out := make([]bool, 0, len(cells)-1)
	out = append(out, cells[:i]...)
	return append(out, cells[i+1:]...)
}

func indexOf(list []flowRow, name string) int {
	for i, row := range list {
		if row.name == name {
			return i
		}
	}
	return -1
}

func columnWidths(header []string, rows [][]string) []int {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = visibleLen(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && visibleLen(cell) > widths[i] {
				widths[i] = visibleLen(cell)
			}
		}
	}
	return widths
}

// joinCells pads each cell to its column (right-aligned where asked) and
// applies paint after padding so colours never disturb alignment.
func joinCells(cells []string, widths []int, right []bool, paint func(col int, cell string) string) string {
	var b strings.Builder
	for i, cell := range cells {
		if i >= len(widths) {
			break
		}
		gap := widths[i] - visibleLen(cell)
		if gap < 0 {
			gap = 0
		}
		padded := cell + strings.Repeat(" ", gap)
		if i < len(right) && right[i] {
			padded = strings.Repeat(" ", gap) + cell
		}
		if paint != nil {
			padded = paint(i, padded)
		}
		b.WriteString(padded)
		if i < len(cells)-1 {
			b.WriteString("  ")
		}
	}
	return b.String()
}

// visibleLen counts printed cells, skipping ANSI escape sequences.
func visibleLen(s string) int {
	n := 0
	state := 0 // 0 text, 1 after ESC, 2 inside CSI
	for _, r := range s {
		switch state {
		case 1:
			if r == '[' {
				state = 2
			} else {
				state = 0
			}
		case 2:
			if r >= 0x40 && r <= 0x7e {
				state = 0
			}
		default:
			if r == 0x1b {
				state = 1
			} else {
				n++
			}
		}
	}
	return n
}

// clip cuts s to width printed cells, keeping escape sequences and closing
// any colour that was open.
func clip(s string, width int) string {
	if visibleLen(s) <= width {
		return s
	}
	var b strings.Builder
	n := 0
	state := 0 // 0 text, 1 after ESC, 2 inside CSI
	for _, r := range s {
		switch state {
		case 1:
			b.WriteRune(r)
			if r == '[' {
				state = 2
			} else {
				state = 0
			}
		case 2:
			b.WriteRune(r)
			if r >= 0x40 && r <= 0x7e {
				state = 0
			}
		default:
			if r == 0x1b {
				state = 1
				b.WriteRune(r)
			} else if n < width {
				b.WriteRune(r)
				n++
			}
		}
	}
	b.WriteString(ansiReset)
	return b.String()
}

// readKeys turns terminal input into key names. In raw mode keys arrive
// byte by byte (arrow keys as escape sequences); otherwise a whole line is
// read and its first character is the key (Enter alone is "enter").
func readKeys(in io.Reader, raw bool, keys chan<- string) {
	reader := bufio.NewReader(in)
	if !raw {
		for {
			line, err := reader.ReadString('\n')
			if err != nil && line == "" {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				keys <- "enter"
			} else {
				keys <- line[:1]
			}
		}
	}
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return
		}
		switch b {
		case 0x03:
			keys <- "ctrl-c"
		case 0x04:
			keys <- "ctrl-d"
		case 0x15:
			keys <- "ctrl-u"
		case '\r', '\n':
			keys <- "enter"
		case 0x1b:
			keys <- readEscape(reader)
		default:
			keys <- string(rune(b))
		}
	}
}

// readEscape decodes the rest of an escape sequence (CSI arrows, paging).
func readEscape(reader *bufio.Reader) string {
	if reader.Buffered() == 0 {
		// A lone Escape key; peek would block until the next key.
		return "esc"
	}
	b, err := reader.ReadByte()
	if err != nil || b != '[' {
		return "esc"
	}
	var seq []byte
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return "esc"
		}
		seq = append(seq, b)
		if b >= 0x40 && b <= 0x7e {
			break
		}
	}
	switch string(seq) {
	case "A":
		return "up"
	case "B":
		return "down"
	case "C":
		return "right"
	case "D":
		return "left"
	case "H":
		return "home"
	case "F":
		return "end"
	case "5~":
		return "pgup"
	case "6~":
		return "pgdn"
	}
	return "esc"
}
