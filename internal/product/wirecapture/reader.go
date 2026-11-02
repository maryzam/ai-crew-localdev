package wirecapture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/product/evidence"
)

type Summary struct {
	Enabled     *bool
	Exchanges   int
	Incomplete  int
	Malformed   int
	Failed      uint64
	WireBytes   int64
	StoredBytes int64
}

func Summarize(root *evidence.Root, run string) (Summary, error) {
	var summary Summary
	dir, err := root.RunDir(run)
	if err != nil {
		return summary, err
	}
	wire := filepath.Join(dir, evidence.WireDir)
	summary.StoredBytes, err = evidence.Size(wire)
	if err != nil {
		return summary, err
	}
	summary.Malformed, err = evidence.EachLine(filepath.Join(wire, exchangesFile), func(line []byte) error {
		var exchange Exchange
		if err := json.Unmarshal(line, &exchange); err != nil {
			return err
		}
		summary.Exchanges++
		summary.WireBytes += exchange.Request.Bytes + exchange.Response.Bytes
		if !exchange.Request.Complete || !exchange.Response.Complete {
			summary.Incomplete++
		}
		return nil
	})
	if err != nil {
		return summary, err
	}
	failures := map[string]uint64{}
	_, err = root.RunEvents(run, func(event evidence.RunEvent) {
		if event.Event != CaptureEvent {
			return
		}
		var state snapshot
		if json.Unmarshal(event.Data, &state) != nil {
			return
		}
		enabled := state.Enabled
		summary.Enabled = &enabled
		failures[state.Instance] = state.Failed + uint64(max(state.Unfinished, 0))
	})
	for _, failed := range failures {
		summary.Failed += failed
	}
	return summary, err
}

var ErrUnreadableRecords = errors.New("wire capture has unreadable records")

func Each(root *evidence.Root, run string, visit func(Exchange, []byte, []byte) error) error {
	dir, err := root.RunDir(run)
	if err != nil {
		return err
	}
	wire := filepath.Join(dir, evidence.WireDir)
	pack, err := os.Open(filepath.Join(wire, packFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("open wire chunks: %w", err)
	}
	if err == nil {
		defer func() { _ = pack.Close() }()
	}
	var failure error
	malformed, err := evidence.EachLine(filepath.Join(wire, exchangesFile), func(line []byte) error {
		var exchange Exchange
		if err := json.Unmarshal(line, &exchange); err != nil {
			return err
		}
		if failure != nil {
			return nil
		}
		request, err := readBody(pack, exchange.Request)
		if err != nil {
			failure = fmt.Errorf("exchange %s request: %w", exchange.ID, err)
			return nil
		}
		response, err := readBody(pack, exchange.Response)
		if err != nil {
			failure = fmt.Errorf("exchange %s response: %w", exchange.ID, err)
			return nil
		}
		failure = visit(exchange, request, response)
		return nil
	})
	if malformed > 0 {
		failure = errors.Join(failure, fmt.Errorf("%w: %d omitted", ErrUnreadableRecords, malformed))
	}
	return errors.Join(err, failure)
}

func readBody(pack *os.File, body Body) ([]byte, error) {
	if pack == nil && len(body.Extents) > 0 {
		return nil, errors.New("wire chunks are missing")
	}
	var content []byte
	for _, location := range body.Extents {
		if location[1] < 0 || int64(len(content))+location[1] > body.Stored {
			return nil, errors.New("recorded extents exceed the stored size")
		}
		part := make([]byte, location[1])
		if _, err := pack.ReadAt(part, location[0]); err != nil {
			return nil, fmt.Errorf("read wire chunks: %w", err)
		}
		content = append(content, part...)
	}
	digest := sha256.Sum256(content)
	if int64(len(content)) != body.Stored || hex.EncodeToString(digest[:]) != body.SHA256 {
		return nil, errors.New("stored bytes do not match the recorded SHA-256")
	}
	return content, nil
}
