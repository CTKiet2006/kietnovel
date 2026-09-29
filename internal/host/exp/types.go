// Package exp implements the export of completed chapters.
//
// Symmetric with imp/: pure local IO, no LLM dependency, no change to store state. An export may run
// concurrently with the Engine (read-only access to Progress plus the chapter finals), so it is a cross-cutting capability.
//
// TXT and EPUB are supported today.
package exp

import "github.com/CTKiet2006/kietnovel/internal/store"

// Format identifies the export format.
type Format string

const (
	// FormatTXT is plain text output.
	FormatTXT Format = "txt"
	// FormatEPUB is a standard EPUB 3 container (zip + xhtml).
	FormatEPUB Format = "epub"
)

// Options controls export behaviour. The zero value means "export the whole book to the default path, erroring when the file exists".
//
// Layout: 《book title》 -> volume separator -> chapter body (the title is wrapped in CJK book-title marks). Two kinds of
// creative blueprint, holding backstage metadata such as target readers / core appeal / writing taboos, meant for the
// author and the engine rather than as a reader's preface); and arc separators (from a reader's point of view an arc is an over-fine internal structure). The book title and the volume separators are always kept.
type Options struct {
	// An empty Format is inferred from the OutPath suffix (.txt -> TXT, .epub -> EPUB);
	// when OutPath is empty too, it falls back to FormatTXT. SDK callers can set it explicitly to skip inference.
	Format Format

	// OutPath is the output file path; empty means {novelDir}/{BookMetadata.Title}.{ext}.
	OutPath string

	// From / To is the chapter range, inclusive at both ends. 0 means "from chapter 1" / "to the last chapter".
	// Unfinished chapters inside the range are skipped and written to Result.Skipped, which is not an error.
	From, To int

	// Overwrite says whether an existing file is replaced; the default refuses.
	Overwrite bool
}

// Deps are the dependencies Run needs. Only store; exporting needs no LLM, prompt or bundle.
type Deps struct {
	Store *store.Store
}

// Result is the summary of one successful export.
type Result struct {
	// Path is the file path actually written (absolute, or the relative path the caller passed).
	Path string
	// Chapters is the number of chapters actually written.
	Chapters int
	// Bytes is the file size in bytes (UTF-8).
	Bytes int
	// Skipped lists the chapter numbers that were in the requested range but unfinished.
	Skipped []int
}
