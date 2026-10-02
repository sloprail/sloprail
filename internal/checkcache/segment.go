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

//go:generate sh -c "SR_GEN_DICT=1 go test -run TestGenerateDefaultDict ."

// A segment is one write-once blob of per-record zstd frames (all with the same
// dictionary), named by the sha256 of its bytes, plus a sorted index of
// key16 -> (offset, length) so a lookup inflates only the frames it needs.
//
// idx layout: "SRIDX001" | dictSha[32] | n u32 | n x (key16 | off u32 | len u32), big endian.

const (
	idxMagic  = "SRIDX001"
	keyLen    = 16
	idxEntry  = keyLen + 8
	idxHeader = len(idxMagic) + 32 + 4
)

type segIdx struct {
	Name   string // sha256 hex of the .zst bytes
	ZstOid string // git blob id of seg/<Name>.zst
	Dict   string // sha256 hex of the dictionary
	Keys   [][keyLen]byte
	Offs   []uint32
	Lens   []uint32
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

// zdict is a loaded dictionary with its codecs.
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

func marshalResult(r Result) ([]byte, error) { return json.Marshal(r) }

// encodeSegment compresses results (duplicate keys within the batch resolve by
// Newer) into a segment and its index.
func encodeSegment(results []Result, d *zdict) (name string, zst, idx []byte, err error) {
	byID := map[string]Result{}
	for _, r := range results {
		id := r.Key.ID()
		if p, ok := byID[id]; ok && !Newer(r, p) {
			continue
		}
		byID[id] = r
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids) // hex order == key16 byte order
	var data, ix bytes.Buffer
	dsha, _ := hex.DecodeString(d.sha)
	ix.WriteString(idxMagic)
	ix.Write(dsha)
	_ = binary.Write(&ix, binary.BigEndian, uint32(len(ids)))
	for _, id := range ids {
		raw, err := marshalResult(byID[id])
		if err != nil {
			return "", nil, nil, err
		}
		frame := d.enc.EncodeAll(raw, nil)
		k, err := key16(id)
		if err != nil {
			return "", nil, nil, err
		}
		ix.Write(k[:])
		_ = binary.Write(&ix, binary.BigEndian, uint32(data.Len()))
		_ = binary.Write(&ix, binary.BigEndian, uint32(len(frame)))
		data.Write(frame)
	}
	sum := sha256.Sum256(data.Bytes())
	return hex.EncodeToString(sum[:]), data.Bytes(), ix.Bytes(), nil
}

func parseIdx(name, oid string, b []byte) (*segIdx, error) {
	if len(b) < idxHeader || string(b[:len(idxMagic)]) != idxMagic {
		return nil, fmt.Errorf("%w: index of %s malformed", ErrCorrupt, name)
	}
	n := int(binary.BigEndian.Uint32(b[idxHeader-4 : idxHeader]))
	if len(b) != idxHeader+n*idxEntry {
		return nil, fmt.Errorf("%w: index of %s has wrong length", ErrCorrupt, name)
	}
	s := &segIdx{Name: name, ZstOid: oid, Dict: hex.EncodeToString(b[len(idxMagic) : len(idxMagic)+32]),
		Keys: make([][keyLen]byte, n), Offs: make([]uint32, n), Lens: make([]uint32, n)}
	p := idxHeader
	for i := 0; i < n; i++ {
		copy(s.Keys[i][:], b[p:p+keyLen])
		s.Offs[i] = binary.BigEndian.Uint32(b[p+keyLen:])
		s.Lens[i] = binary.BigEndian.Uint32(b[p+keyLen+4:])
		if i > 0 && bytes.Compare(s.Keys[i-1][:], s.Keys[i][:]) >= 0 {
			return nil, fmt.Errorf("%w: index of %s is not sorted", ErrCorrupt, name)
		}
		p += idxEntry
	}
	return s, nil
}

// decodeAt inflates entry i of a segment whose verified bytes are blob.
func (s *segIdx) decodeAt(blob []byte, i int, d *zdict) (Result, error) {
	off, ln := int(s.Offs[i]), int(s.Lens[i])
	if off < 0 || ln <= 0 || off+ln > len(blob) {
		return Result{}, fmt.Errorf("%w: %s entry out of bounds", ErrCorrupt, s.Name)
	}
	raw, err := d.dec.DecodeAll(blob[off:off+ln], nil)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %s: %v", ErrCorrupt, s.Name, err)
	}
	var r Result
	if err := json.Unmarshal(raw, &r); err != nil {
		return Result{}, fmt.Errorf("%w: %s: %v", ErrCorrupt, s.Name, err)
	}
	if k, _ := key16(r.Key.ID()); k != s.Keys[i] {
		return Result{}, fmt.Errorf("%w: %s record does not match its index key", ErrCorrupt, s.Name)
	}
	return r, nil
}

func verifySegment(name string, blob []byte) error {
	sum := sha256.Sum256(blob)
	if hex.EncodeToString(sum[:]) != name {
		return fmt.Errorf("%w: seg/%s.zst does not hash to its name", ErrCorrupt, name)
	}
	return nil
}
