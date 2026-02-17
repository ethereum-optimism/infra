package runner

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// ParseMultiPackageEvents reads a raw_go_events.log (possibly merged from multiple
// parallel nodes) and reconstructs one TestResult per package.  Each package is
// treated as a "run-all" result whose subtests are the individual Test* functions
// discovered in the event stream.
//
// The returned slice is sorted by package name for deterministic output.
func ParseMultiPackageEvents(r io.Reader) ([]*types.TestResult, error) {
	if r == nil {
		return nil, fmt.Errorf("reader is nil")
	}

	// Group raw JSON lines by package so we can parse each package independently.
	pkgLines := make(map[string][][]byte)
	scanner := bufio.NewScanner(r)
	// Allow long lines (some output events can be large)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		event, err := parseTestEvent(line)
		if err != nil {
			continue // skip non-JSON lines
		}
		pkg := event.Package
		if pkg == "" {
			continue
		}
		// Copy the line since scanner reuses the buffer
		lineCopy := make([]byte, len(line))
		copy(lineCopy, line)
		pkgLines[pkg] = append(pkgLines[pkg], lineCopy)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading events: %w", err)
	}

	if len(pkgLines) == 0 {
		return nil, nil
	}

	// Parse each package's events into a TestResult
	parser := NewOutputParser()
	var results []*types.TestResult

	// Sort package names for deterministic order
	pkgNames := make([]string, 0, len(pkgLines))
	for pkg := range pkgLines {
		pkgNames = append(pkgNames, pkg)
	}
	sort.Strings(pkgNames)

	for _, pkg := range pkgNames {
		lines := pkgLines[pkg]
		// Reconstruct an io.Reader from the grouped lines
		combined := strings.NewReader(joinLines(lines))

		meta := types.ValidatorMetadata{
			Package: pkg,
			RunAll:  true,
			Gate:    "gateless",
		}

		result := parser.Parse(combined, meta)
		if result != nil {
			results = append(results, result)
		}
	}

	return results, nil
}

// joinLines joins byte slices with newlines into a single string.
func joinLines(lines [][]byte) string {
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.Write(line)
	}
	return b.String()
}
