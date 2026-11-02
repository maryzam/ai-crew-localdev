package wirecapture

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/product/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/product/securefile"
	"golang.org/x/sys/unix"
)

const (
	packFile        = "chunks.bin"
	exchangesFile   = "exchanges.jsonl"
	packBufferBytes = 1 << 20
)

type Body struct {
	Bytes    int64      `json:"bytes"`
	Stored   int64      `json:"stored"`
	Complete bool       `json:"complete"`
	SHA256   string     `json:"sha256,omitempty"`
	Extents  [][2]int64 `json:"extents,omitempty"`
}

type Exchange struct {
	ID              string              `json:"id"`
	Agent           string              `json:"agent"`
	Method          string              `json:"method"`
	Path            string              `json:"path"`
	Started         time.Time           `json:"started"`
	FirstByteMS     int64               `json:"first_byte_ms"`
	DurationMS      int64               `json:"duration_ms"`
	Status          int                 `json:"status"`
	Interrupted     bool                `json:"interrupted,omitempty"`
	RequestHeaders  map[string][]string `json:"request_headers,omitempty"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	Request         Body                `json:"request"`
	Response        Body                `json:"response"`
}

type extent struct {
	offset int64
	length int64
}

type store struct {
	root     *evidence.Root
	run      string
	mu       sync.Mutex
	known    map[[sha256.Size]byte]extent
	packSize int64
}

func newStore(root *evidence.Root, run string) *store {
	return &store{root: root, run: run, known: map[[sha256.Size]byte]extent{}}
}

func (s *store) write(exchange *Exchange, request, response []byte) error {
	requestPlan := planBody(request, &exchange.Request)
	responsePlan := planBody(response, &exchange.Response)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.WithRunLock(s.run, func(dir string) error {
		wire := filepath.Join(dir, evidence.WireDir)
		if err := securefile.EnsureOwnerOnlyDir(wire); err != nil {
			return err
		}
		pack, size, err := openPack(filepath.Join(wire, packFile))
		if err != nil {
			return err
		}
		defer func() { _ = pack.Close() }()
		if size != s.packSize {
			s.known = map[[sha256.Size]byte]extent{}
			s.packSize = size
		}
		s.packSize = -1
		next, err := s.append(pack, size, []plannedBody{{requestPlan, &exchange.Request}, {responsePlan, &exchange.Response}})
		if err != nil {
			return err
		}
		s.packSize = next
		line, err := json.Marshal(exchange)
		if err != nil {
			return fmt.Errorf("encode wire exchange: %w", err)
		}
		return evidence.AppendLine(filepath.Join(wire, exchangesFile), line, false)
	})
}

func (s *store) append(pack *os.File, next int64, bodies []plannedBody) (int64, error) {
	writer := bufio.NewWriterSize(pack, packBufferBytes)
	fresh := map[[sha256.Size]byte]extent{}
	for _, body := range bodies {
		for _, chunk := range body.chunks {
			location, stored := s.known[chunk.digest]
			if !stored {
				location, stored = fresh[chunk.digest]
			}
			if !stored {
				location = extent{offset: next, length: int64(len(chunk.data))}
				if _, err := writer.Write(chunk.data); err != nil {
					return 0, fmt.Errorf("append wire chunks: %w", err)
				}
				fresh[chunk.digest] = location
				next += location.length
			}
			body.record.addExtent(location)
		}
	}
	if err := writer.Flush(); err != nil {
		return 0, fmt.Errorf("append wire chunks: %w", err)
	}
	if err := pack.Close(); err != nil {
		return 0, fmt.Errorf("close wire chunks: %w", err)
	}
	for digest, location := range fresh {
		s.known[digest] = location
	}
	return next, nil
}

func openPack(path string) (*os.File, int64, error) {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("open wire chunks: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("inspect wire chunks: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Getuid()) || stat.Mode&0o077 != 0 {
		_ = file.Close()
		return nil, 0, errors.New("wire chunks must be an owner-only regular file")
	}
	return file, stat.Size, nil
}

type plannedChunk struct {
	digest [sha256.Size]byte
	data   []byte
}

type plannedBody struct {
	chunks []plannedChunk
	record *Body
}

func planBody(content []byte, body *Body) []plannedChunk {
	whole := sha256.Sum256(content)
	body.SHA256 = hex.EncodeToString(whole[:])
	body.Stored = int64(len(content))
	var chunks []plannedChunk
	splitChunks(content, func(chunk []byte) {
		chunks = append(chunks, plannedChunk{digest: sha256.Sum256(chunk), data: chunk})
	})
	return chunks
}

func (b *Body) addExtent(location extent) {
	count := len(b.Extents)
	if count > 0 && b.Extents[count-1][0]+b.Extents[count-1][1] == location.offset {
		b.Extents[count-1][1] += location.length
		return
	}
	b.Extents = append(b.Extents, [2]int64{location.offset, location.length})
}
