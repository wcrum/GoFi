// Package spool is a bounded, crash-safe, on-disk FIFO of byte records used
// as the spill buffer behind a sink queue: when the destination is
// unreachable the in-memory queue overflows into the spool, and the spool
// drains back once delivery resumes, so short outages do not lose data.
//
// Layout: <dir>/seg-<seq>.log segment files, each a run of records
// [u32 length][u32 crc32][payload], plus <dir>/cursor holding the ack
// position "seq offset". Records are appended to the newest segment; readers
// Peek a batch from the oldest unacked position and Ack after delivery.
// A crash between Peek and Ack redelivers (at-least-once). A torn tail
// record (power loss mid-append) is detected by length/CRC and truncated on
// Open. The spool never exceeds MaxBytes: with DropOldest the oldest segment
// is deleted to make room, otherwise Push returns ErrFull.
//
// It is single-process: one *Queue per directory.
package spool

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wcrum/gofi/atomicfile"
)

const (
	headerSize    = 8
	segPrefix     = "seg-"
	segSuffix     = ".log"
	cursorFile    = "cursor"
	maxRecordSize = 64 << 20
)

var (
	// ErrFull is returned by Push when MaxBytes would be exceeded and
	// DropOldest is false.
	ErrFull = errors.New("spool: full")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("spool: closed")
	// ErrTooLarge is returned for a record larger than the segment size.
	ErrTooLarge = errors.New("spool: record too large")
)

// Options tune a Queue. Zero values take the defaults noted.
type Options struct {
	// MaxBytes bounds the total size of segment files on disk (default 64 MiB).
	MaxBytes int64
	// SegmentBytes is the size at which a new segment starts (default 4 MiB,
	// clamped to MaxBytes/4 so at least four segments fit).
	SegmentBytes int64
	// SyncInterval batches fsyncs of appended records: 0 fsyncs every Push
	// (safest, hardest on flash), otherwise the file is synced at most once
	// per interval and on Close/Sync. Acks are always fsynced.
	SyncInterval time.Duration
	// DropOldest deletes the oldest segment when full instead of refusing.
	DropOldest bool
}

func (o Options) withDefaults() Options {
	if o.MaxBytes <= 0 {
		o.MaxBytes = 64 << 20
	}
	if o.SegmentBytes <= 0 {
		o.SegmentBytes = 4 << 20
	}
	if o.SegmentBytes > o.MaxBytes/4 {
		o.SegmentBytes = max(o.MaxBytes/4, 64<<10)
	}
	return o
}

// Record is one queued payload with its position, for Ack.
type Record struct {
	Seq    uint64 // segment sequence number
	Offset int64  // file offset just past this record
	Data   []byte
}

type segment struct {
	seq  uint64
	size int64
	// records counts complete records in the segment (after truncation).
	records int
}

// Queue is the spool. Methods are safe for concurrent use.
type Queue struct {
	dir  string
	opts Options

	mu       sync.Mutex
	segments []*segment // ascending seq; last is the active writer
	active   *os.File
	dirty    bool
	lastSync time.Time
	closed   bool

	// read position
	cursorSeq uint64
	cursorOff int64
	reader    *os.File
	readerSeq uint64
	readerOff int64

	pending int   // unacked records
	bytes   int64 // total size of segment files

	// Dropped counts records discarded by DropOldest since Open.
	Dropped uint64
}

// Open opens or creates a spool in dir and recovers its state.
func Open(dir string, opts Options) (*Queue, error) {
	opts = opts.withDefaults()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	q := &Queue{dir: dir, opts: opts}
	if err := q.recover(); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Queue) segPath(seq uint64) string {
	return filepath.Join(q.dir, fmt.Sprintf("%s%016d%s", segPrefix, seq, segSuffix))
}

func (q *Queue) recover() error {
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		return err
	}
	var seqs []uint64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, segPrefix) || !strings.HasSuffix(name, segSuffix) {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(name, segPrefix), segSuffix), 10, 64)
		if err != nil {
			continue
		}
		seqs = append(seqs, n)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	// Cursor.
	if b, err := os.ReadFile(filepath.Join(q.dir, cursorFile)); err == nil {
		fields := strings.Fields(string(b))
		if len(fields) == 2 {
			q.cursorSeq, _ = strconv.ParseUint(fields[0], 10, 64)
			q.cursorOff, _ = strconv.ParseInt(fields[1], 10, 64)
		}
	}

	for i, seq := range seqs {
		path := q.segPath(seq)
		if seq < q.cursorSeq {
			// Fully acked but not yet removed (crash between ack and delete).
			_ = os.Remove(path)
			continue
		}
		last := i == len(seqs)-1
		s, err := q.scanSegment(seq, path, last)
		if err != nil {
			return err
		}
		if s.size == 0 && !last {
			_ = os.Remove(path)
			continue
		}
		q.segments = append(q.segments, s)
		q.bytes += s.size
	}
	if len(q.segments) == 0 {
		q.cursorSeq, q.cursorOff = 0, 0
		if err := q.roll(1); err != nil {
			return err
		}
		return q.writeCursor()
	}
	// Cursor must sit inside the head segment.
	head := q.segments[0]
	if q.cursorSeq != head.seq {
		q.cursorSeq, q.cursorOff = head.seq, 0
	} else if q.cursorOff > head.size {
		q.cursorOff = head.size // tail truncated past a stale cursor
	}
	// Count pending: records in head after cursor + all records in later segments.
	n, err := q.countFrom(head, q.cursorOff)
	if err != nil {
		return err
	}
	q.pending = n
	for _, s := range q.segments[1:] {
		q.pending += s.records
	}
	// Reopen the last segment for append.
	tail := q.segments[len(q.segments)-1]
	f, err := os.OpenFile(q.segPath(tail.seq), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	q.active = f
	q.readerSeq, q.readerOff = q.cursorSeq, q.cursorOff
	return nil
}

// scanSegment validates a segment, truncating a torn tail when allowed.
func (q *Queue) scanSegment(seq uint64, path string, truncateTorn bool) (*segment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	var off int64
	var hdr [headerSize]byte
	s := &segment{seq: seq}
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if err == io.EOF {
				break
			}
			if err == io.ErrUnexpectedEOF {
				break // torn header
			}
			return nil, err
		}
		n := binary.LittleEndian.Uint32(hdr[:4])
		sum := binary.LittleEndian.Uint32(hdr[4:])
		if n > maxRecordSize {
			break // garbage header: treat as torn
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			break // torn payload
		}
		if crc32.ChecksumIEEE(buf) != sum {
			break // bit rot or torn write in the middle
		}
		off += headerSize + int64(n)
		s.records++
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() != off {
		if !truncateTorn {
			// A non-tail segment with garbage after valid records: readers
			// stop at off; the rest is unreachable and reclaimed on delete.
			s.size = off
			return s, nil
		}
		if err := os.Truncate(path, off); err != nil {
			return nil, fmt.Errorf("spool: truncate torn tail of %s: %w", path, err)
		}
	}
	s.size = off
	return s, nil
}

func (q *Queue) countFrom(s *segment, off int64) (int, error) {
	if off == 0 {
		return s.records, nil
	}
	f, err := os.Open(q.segPath(s.seq))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	r := bufio.NewReader(io.LimitReader(f, s.size-off))
	count := 0
	var hdr [headerSize]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			break
		}
		n := binary.LittleEndian.Uint32(hdr[:4])
		if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
			break
		}
		count++
	}
	return count, nil
}

func (q *Queue) roll(seq uint64) error {
	if q.active != nil {
		if err := q.syncActive(); err != nil {
			return err
		}
		_ = q.active.Close()
		q.active = nil
	}
	f, err := os.OpenFile(q.segPath(seq), os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	q.active = f
	q.segments = append(q.segments, &segment{seq: seq})
	if len(q.segments) == 1 {
		q.cursorSeq, q.cursorOff = seq, 0
		q.readerSeq, q.readerOff = seq, 0
	}
	return atomicfile.SyncDir(q.dir)
}

func (q *Queue) syncActive() error {
	if q.active == nil || !q.dirty {
		return nil
	}
	if err := q.active.Sync(); err != nil {
		return err
	}
	q.dirty = false
	q.lastSync = time.Now()
	return nil
}

// Push appends a record. It returns ErrFull when the spool is at MaxBytes
// and DropOldest is off, ErrTooLarge when the record cannot fit a segment.
func (q *Queue) Push(data []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	need := int64(headerSize + len(data))
	if need > q.opts.SegmentBytes {
		return ErrTooLarge
	}
	for q.bytes+need > q.opts.MaxBytes {
		if !q.opts.DropOldest {
			return ErrFull
		}
		if err := q.dropOldest(); err != nil {
			return err
		}
	}
	tail := q.segments[len(q.segments)-1]
	if tail.size+need > q.opts.SegmentBytes && tail.size > 0 {
		if err := q.roll(tail.seq + 1); err != nil {
			return err
		}
		tail = q.segments[len(q.segments)-1]
	}
	var hdr [headerSize]byte
	binary.LittleEndian.PutUint32(hdr[:4], uint32(len(data)))
	binary.LittleEndian.PutUint32(hdr[4:], crc32.ChecksumIEEE(data))
	buf := make([]byte, 0, need)
	buf = append(buf, hdr[:]...)
	buf = append(buf, data...)
	if _, err := q.active.Write(buf); err != nil {
		return err
	}
	tail.size += need
	tail.records++
	q.bytes += need
	q.pending++
	q.dirty = true
	if q.opts.SyncInterval == 0 || time.Since(q.lastSync) >= q.opts.SyncInterval {
		return q.syncActive()
	}
	return nil
}

// dropOldest removes the head segment (or, when only the active segment
// exists, rolls first so the active one becomes droppable).
func (q *Queue) dropOldest() error {
	if len(q.segments) == 1 {
		if q.segments[0].size == 0 {
			return ErrFull // nothing to drop; MaxBytes smaller than one record
		}
		if err := q.roll(q.segments[0].seq + 1); err != nil {
			return err
		}
	}
	head := q.segments[0]
	unread, err := q.countFrom(head, q.cursorOff)
	if err != nil {
		return err
	}
	q.Dropped += uint64(unread)
	q.pending -= unread
	q.bytes -= head.size
	if q.reader != nil && q.readerSeq == head.seq {
		_ = q.reader.Close()
		q.reader = nil
	}
	_ = os.Remove(q.segPath(head.seq))
	q.segments = q.segments[1:]
	q.cursorSeq, q.cursorOff = q.segments[0].seq, 0
	q.readerSeq, q.readerOff = q.cursorSeq, 0
	return q.writeCursor()
}

// Peek returns up to max records (and at most maxBytes of payload, when > 0)
// from the oldest unacked position without removing them. Repeated Peeks
// without Ack return the same records.
func (q *Queue) Peek(max int, maxBytes int) ([]Record, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil, ErrClosed
	}
	if max <= 0 {
		max = 1
	}
	// Make appended-but-unsynced bytes visible to the reader path: the
	// reader reads the file, which already holds them (page cache).
	q.readerSeq, q.readerOff = q.cursorSeq, q.cursorOff
	var out []Record
	var total int
	for len(out) < max {
		s := q.findSegment(q.readerSeq)
		if s == nil {
			break
		}
		if q.readerOff >= s.size {
			// Advance to the next segment.
			next := q.nextSegment(s.seq)
			if next == nil {
				break
			}
			q.readerSeq, q.readerOff = next.seq, 0
			continue
		}
		rec, err := q.readAt(s, q.readerOff)
		if err != nil {
			return out, err
		}
		if maxBytes > 0 && total > 0 && total+len(rec.Data) > maxBytes {
			break
		}
		total += len(rec.Data)
		out = append(out, rec)
		q.readerOff = rec.Offset
	}
	return out, nil
}

func (q *Queue) findSegment(seq uint64) *segment {
	for _, s := range q.segments {
		if s.seq == seq {
			return s
		}
	}
	return nil
}

func (q *Queue) nextSegment(seq uint64) *segment {
	for _, s := range q.segments {
		if s.seq > seq {
			return s
		}
	}
	return nil
}

func (q *Queue) readAt(s *segment, off int64) (Record, error) {
	if q.reader == nil || q.readerSeq != s.seq || q.reader.Name() != q.segPath(s.seq) {
		if q.reader != nil {
			_ = q.reader.Close()
		}
		f, err := os.Open(q.segPath(s.seq))
		if err != nil {
			return Record{}, err
		}
		q.reader = f
	}
	var hdr [headerSize]byte
	if _, err := q.reader.ReadAt(hdr[:], off); err != nil {
		return Record{}, fmt.Errorf("spool: read header: %w", err)
	}
	n := binary.LittleEndian.Uint32(hdr[:4])
	buf := make([]byte, n)
	if _, err := q.reader.ReadAt(buf, off+headerSize); err != nil {
		return Record{}, fmt.Errorf("spool: read payload: %w", err)
	}
	if crc32.ChecksumIEEE(buf) != binary.LittleEndian.Uint32(hdr[4:]) {
		return Record{}, fmt.Errorf("spool: crc mismatch in segment %d at %d", s.seq, off)
	}
	return Record{Seq: s.seq, Offset: off + headerSize + int64(n), Data: buf}, nil
}

// Ack marks every record up to and including r as delivered, durably.
// Fully consumed segments are deleted.
func (q *Queue) Ack(r Record) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	if r.Seq < q.cursorSeq || (r.Seq == q.cursorSeq && r.Offset <= q.cursorOff) {
		return nil // already acked
	}
	// Count what we are acking and drop whole segments before r.Seq.
	for len(q.segments) > 1 && q.segments[0].seq < r.Seq {
		head := q.segments[0]
		n, err := q.countFrom(head, q.cursorOff)
		if err != nil {
			return err
		}
		q.pending -= n
		q.bytes -= head.size
		if q.reader != nil && q.readerSeq == head.seq {
			_ = q.reader.Close()
			q.reader = nil
		}
		_ = os.Remove(q.segPath(head.seq))
		q.segments = q.segments[1:]
		q.cursorSeq, q.cursorOff = q.segments[0].seq, 0
	}
	s := q.findSegment(r.Seq)
	if s == nil {
		return fmt.Errorf("spool: ack of unknown segment %d", r.Seq)
	}
	off := min(r.Offset, s.size)
	// Count records between cursorOff and off within s.
	before, err := q.countFrom(s, q.cursorOff)
	if err != nil {
		return err
	}
	after, err := q.countFrom(s, off)
	if err != nil {
		return err
	}
	q.pending -= before - after
	q.cursorSeq, q.cursorOff = s.seq, off
	// When the head segment is fully consumed and is not the active
	// writer, delete it now so flash is reclaimed promptly.
	if len(q.segments) > 1 && q.segments[0] == s && off >= s.size {
		if q.reader != nil && q.readerSeq == s.seq {
			_ = q.reader.Close()
			q.reader = nil
		}
		_ = os.Remove(q.segPath(s.seq))
		q.bytes -= s.size
		q.segments = q.segments[1:]
		q.cursorSeq, q.cursorOff = q.segments[0].seq, 0
	}
	q.readerSeq, q.readerOff = q.cursorSeq, q.cursorOff
	return q.writeCursor()
}

func (q *Queue) writeCursor() error {
	data := []byte(strconv.FormatUint(q.cursorSeq, 10) + " " + strconv.FormatInt(q.cursorOff, 10) + "\n")
	err := atomicfile.Write(filepath.Join(q.dir, cursorFile), data, atomicfile.Options{NoBackup: true})
	if errors.Is(err, atomicfile.ErrUnchanged) {
		return nil
	}
	return err
}

// Pending is the number of unacked records.
func (q *Queue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pending
}

// Bytes is the total size of segment files on disk.
func (q *Queue) Bytes() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.bytes
}

// Sync flushes appended records to stable storage.
func (q *Queue) Sync() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	return q.syncActive()
}

// Close syncs and releases file handles. Pending records stay on disk.
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	var err error
	if q.active != nil {
		err = q.syncActive()
		if cerr := q.active.Close(); err == nil {
			err = cerr
		}
		q.active = nil
	}
	if q.reader != nil {
		_ = q.reader.Close()
		q.reader = nil
	}
	return err
}
