package checkcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/klauspost/compress/dict"
	"github.com/klauspost/compress/zstd"
)

// A segment is one write-once blob of per-record zstd frames (all with the same
// dictionary, or none: below Gc's TrainMin records segments are plain zstd), named by the sha256 of its bytes, plus a sorted index so a lookup inflates
// only the frames it needs. A record is a RUN (a rule evaluated once, with its checks and
// their items); the index has one entry per findable check, so several entries may point
// at one frame: key16 -> (offset, length, position of the check in the run).
//
// idx layout: "SRIDX002" | dictSha[32] (all zero = no dictionary) | n u32 | n x (key16 | off u32 | len u32 | pos u16), big endian.

const (
	idxMagic = "SRIDX002"
	keyLen   = 16
	idxEntry = keyLen + 10
	// runOnly is the position of an index entry that names a run, not one of its checks: it
	// is how a run with nothing findable (an engine failure, an empty range, a script-only
	// rule) is kept, for Runs. A lookup never asks for one: its key is no check's key.
	runOnly   = 0xFFFF
	idxHeader = len(idxMagic) + 32 + 4
)

type segIdx struct {
	Name   string // sha256 hex of the .zst bytes
	ZstOid string // git blob id of seg/<Name>.zst
	Dict   string // sha256 hex of the dictionary; "" = none (plain zstd)
	Keys   [][keyLen]byte
	Offs   []uint32
	Lens   []uint32
	Pos    []uint16
}

func (s *segIdx) find(k [keyLen]byte) int {
	i := sort.Search(len(s.Keys), func(i int) bool { return bytes.Compare(s.Keys[i][:], k[:]) >= 0 })
	if i < len(s.Keys) && s.Keys[i] == k {
		return i
	}
	return -1
}

func key16(id string) ([keyLen]byte, error) {
	var k [keyLen]byte
	b, err := hex.DecodeString(id)
	if err != nil || len(b) < keyLen {
		return k, fmt.Errorf("bad key id %q", id)
	}
	copy(k[:], b)
	return k, nil
}

// zdict is a loaded dictionary with its codecs. sha "" is the no-dictionary codec
// (plain zstd): what segments use until Gc has enough records to train a dictionary.
type zdict struct {
	sha   string
	bytes []byte
	enc   *zstd.Encoder
	dec   *zstd.Decoder
}

func newZdict(b []byte) (*zdict, error) {
	sum := sha256.Sum256(b)
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression), zstd.WithEncoderDict(b), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("%w: dictionary: %v", ErrCorrupt, err)
	}
	dec, err := zstd.NewReader(nil, zstd.WithDecoderDicts(b), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("%w: dictionary: %v", ErrCorrupt, err)
	}
	return &zdict{sha: hex.EncodeToString(sum[:]), bytes: b, enc: enc, dec: dec}, nil
}

func newPlainZdict() (*zdict, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("%w: zstd: %v", ErrCorrupt, err)
	}
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("%w: zstd: %v", ErrCorrupt, err)
	}
	return &zdict{enc: enc, dec: dec}, nil
}

// trainDict builds a dictionary from sample records. The zstd dictionary id is
// derived from the content so two different dictionaries never share one.
func trainDict(samples [][]byte) ([]byte, error) {
	d, err := dict.BuildZstdDict(samples, dict.Options{MaxDictSize: 64 << 10, HashBytes: 6, ZstdDictID: 1, ZstdLevel: zstd.SpeedBetterCompression})
	if err != nil {
		return nil, err
	}
	return stampDictID(d), nil
}

func stampDictID(d []byte) []byte {
	out := append([]byte(nil), d...)
	binary.LittleEndian.PutUint32(out[4:8], 0)
	sum := sha256.Sum256(out)
	id := 32768 + binary.BigEndian.Uint32(sum[:4])%(1<<31-32768)
	binary.LittleEndian.PutUint32(out[4:8], id)
	return out
}

func marshalRun(r Run) ([]byte, error) { return json.Marshal(r) }

// runKeyID is the index key of a run's own entry; the "run" prefix keeps it apart from any
// check key (those hash SchemaVersion first).
func runKeyID(runID string) string {
	sum := sha256.Sum256([]byte("run\x00" + runID))
	return hex.EncodeToString(sum[:])
}

// entry is one findable check of one run.
type entry struct {
	id  string
	run int
	pos int
}

// encodeSegment compresses runs into a segment and its index. Duplicate keys within the
// batch resolve by Newer.
func encodeSegment(runs []Run, d *zdict) (name string, zst, idx []byte, err error) {
	best := map[string]entry{}
	for ri, r := range runs {
		for pi, c := range r.Checks {
			if !Findable(c) {
				continue
			}
			id := r.CheckKey(c).ID()
			if p, ok := best[id]; ok && !Newer(Found{Run: r, Check: c}, Found{Run: runs[p.run], Check: runs[p.run].Checks[p.pos]}) {
				continue
			}
			best[id] = entry{id, ri, pi}
		}
	}
	entries := make([]entry, 0, len(best)+len(runs))
	for _, e := range best {
		entries = append(entries, e)
	}
	seenRun := map[string]bool{}
	for ri, r := range runs {
		if !seenRun[r.ID] {
			seenRun[r.ID] = true
			entries = append(entries, entry{runKeyID(r.ID), ri, runOnly})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id }) // hex order == key16 byte order
	var data, ix bytes.Buffer
	frames := map[int][2]uint32{} // run -> offset, length
	for _, ri := range sortedRuns(entries) {
		raw, err := marshalRun(runs[ri])
		if err != nil {
			return "", nil, nil, err
		}
		frame := d.enc.EncodeAll(raw, nil)
		frames[ri] = [2]uint32{uint32(data.Len()), uint32(len(frame))}
		data.Write(frame)
	}
	dsha := make([]byte, 32) // zero = no dictionary
	if d.sha != "" {
		dsha, _ = hex.DecodeString(d.sha)
	}
	ix.WriteString(idxMagic)
	ix.Write(dsha)
	_ = binary.Write(&ix, binary.BigEndian, uint32(len(entries)))
	for _, e := range entries {
		k, err := key16(e.id)
		if err != nil {
			return "", nil, nil, err
		}
		fr := frames[e.run]
		ix.Write(k[:])
		_ = binary.Write(&ix, binary.BigEndian, fr[0])
		_ = binary.Write(&ix, binary.BigEndian, fr[1])
		_ = binary.Write(&ix, binary.BigEndian, uint16(e.pos))
	}
	sum := sha256.Sum256(data.Bytes())
	return hex.EncodeToString(sum[:]), data.Bytes(), ix.Bytes(), nil
}

// sortedRuns is the runs that own at least one entry, in a stable order (their position in
// the batch), so the same runs always make the same bytes.
func sortedRuns(entries []entry) []int {
	seen := map[int]bool{}
	var out []int
	for _, e := range entries {
		if !seen[e.run] {
			seen[e.run] = true
			out = append(out, e.run)
		}
	}
	sort.Ints(out)
	return out
}

func parseIdx(name, oid string, b []byte) (*segIdx, error) {
	if len(b) < idxHeader || string(b[:len(idxMagic)]) != idxMagic {
		return nil, fmt.Errorf("%w: index of %s malformed", ErrCorrupt, name)
	}
	n := int(binary.BigEndian.Uint32(b[idxHeader-4 : idxHeader]))
	if len(b) != idxHeader+n*idxEntry {
		return nil, fmt.Errorf("%w: index of %s has wrong length", ErrCorrupt, name)
	}
	dict := ""
	if sha := b[len(idxMagic) : len(idxMagic)+32]; !bytes.Equal(sha, make([]byte, 32)) {
		dict = hex.EncodeToString(sha)
	}
	s := &segIdx{Name: name, ZstOid: oid, Dict: dict,
		Keys: make([][keyLen]byte, n), Offs: make([]uint32, n), Lens: make([]uint32, n), Pos: make([]uint16, n)}
	p := idxHeader
	for i := 0; i < n; i++ {
		copy(s.Keys[i][:], b[p:p+keyLen])
		s.Offs[i] = binary.BigEndian.Uint32(b[p+keyLen:])
		s.Lens[i] = binary.BigEndian.Uint32(b[p+keyLen+4:])
		s.Pos[i] = binary.BigEndian.Uint16(b[p+keyLen+8:])
		if i > 0 && bytes.Compare(s.Keys[i-1][:], s.Keys[i][:]) >= 0 {
			return nil, fmt.Errorf("%w: index of %s is not sorted", ErrCorrupt, name)
		}
		p += idxEntry
	}
	return s, nil
}

// decodeRun inflates the run frame of entry i of a segment whose verified bytes are blob.
func (s *segIdx) decodeRun(blob []byte, i int, d *zdict) (Run, error) {
	off, ln := int(s.Offs[i]), int(s.Lens[i])
	if off < 0 || ln <= 0 || off+ln > len(blob) {
		return Run{}, fmt.Errorf("%w: %s entry out of bounds", ErrCorrupt, s.Name)
	}
	raw, err := d.dec.DecodeAll(blob[off:off+ln], nil)
	if err != nil {
		return Run{}, fmt.Errorf("%w: %s: %v", ErrCorrupt, s.Name, err)
	}
	var r Run
	if err := json.Unmarshal(raw, &r); err != nil {
		return Run{}, fmt.Errorf("%w: %s: %v", ErrCorrupt, s.Name, err)
	}
	return r, nil
}

// decodeAt is the check entry i of a segment points at, with its run. The run's own check
// must hash to the index key: a record that does not match its index is corrupt, never a hit.
func (s *segIdx) decodeAt(blob []byte, i int, d *zdict) (Found, error) {
	r, err := s.decodeRun(blob, i, d)
	if err != nil {
		return Found{}, err
	}
	pos := int(s.Pos[i])
	if pos == runOnly {
		return Found{}, fmt.Errorf("%w: %s entry %d names a run, not a check", ErrCorrupt, s.Name, i)
	}
	if pos >= len(r.Checks) {
		return Found{}, fmt.Errorf("%w: %s entry names check %d of a run with %d", ErrCorrupt, s.Name, pos, len(r.Checks))
	}
	c := r.Checks[pos]
	if k, _ := key16(r.CheckKey(c).ID()); k != s.Keys[i] {
		return Found{}, fmt.Errorf("%w: %s record does not match its index key", ErrCorrupt, s.Name)
	}
	return Found{Run: r, Check: c}, nil
}

func verifySegment(name string, blob []byte) error {
	sum := sha256.Sum256(blob)
	if hex.EncodeToString(sum[:]) != name {
		return fmt.Errorf("%w: seg/%s.zst does not hash to its name", ErrCorrupt, name)
	}
	return nil
}
