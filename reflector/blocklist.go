package reflector

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lbryio/reflector.go/store"
	"github.com/lbryio/reflector.go/wallet"

	"github.com/lbryio/lbry.go/extras/errors"
	types "github.com/lbryio/types/go"

	"github.com/golang/protobuf/proto"
	log "github.com/sirupsen/logrus"
)

const blocklistURL = "https://api.lbry.io/file/list_blocked"

func (s *Server) enableBlocklist(b store.Blocklister) {
	// TODO: updateBlocklist should be killed when server is shutting down
	updateBlocklist(b)
	t := time.NewTicker(12 * time.Hour)
	for {
		select {
		case <-s.grp.Ch():
			return
		case <-t.C:
			updateBlocklist(b)
		}
	}
}

func updateBlocklist(b store.Blocklister) {
	values, err := blockedSdHashes()
	if err != nil {
		log.Error(err)
		return
	}

	for _, v := range values {
		if v.Err != nil {
			continue
		}

		err = b.Block(v.Value)
		if err != nil {
			log.Error(err)
		}
	}
}

func blockedSdHashes() (map[string]valOrErr, error) {
	resp, err := http.Get(blocklistURL)
	if err != nil {
		return nil, errors.Err(err)
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			log.Errorln(err)
		}
	}()

	var r struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
		Data    struct {
			Outpoints []string `json:"outpoints"`
		} `json:"data"`
	}

	if err = json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, errors.Err(err)
	}

	if !r.Success {
		return nil, errors.Prefix("list_blocked API call", r.Error)
	}

	return sdHashesForOutpoints(r.Data.Outpoints)
}

type valOrErr struct {
	Value string
	Err   error
}

// sdHashesForOutpoints queries wallet server for the sd hashes in a given outpoints
func sdHashesForOutpoints(outpoints []string) (map[string]valOrErr, error) {
	values := make(map[string]valOrErr)

	node := wallet.NewNode()
	defer node.Shutdown()
	err := node.Connect([]string{
		"victor.lbry.tech:50001",
		//"lbryumx1.lbry.io:50001", // cant use real servers until victor pushes bugfix
		//"lbryumx2.lbry.io:50001",
	}, nil)
	if err != nil {
		return nil, err
	}

	for _, outpoint := range outpoints {
		parts := strings.Split(outpoint, ":")
		if len(parts) != 2 {
			values[outpoint] = valOrErr{Err: errors.Err("invalid outpoint format")}
			continue
		}

		nout, err := strconv.Atoi(parts[1])
		if err != nil {
			values[outpoint] = valOrErr{Err: errors.Prefix("invalid nout", err)}
			continue
		}

		resp, err := node.GetClaimsInTx(parts[0])
		if err != nil {
			values[outpoint] = valOrErr{Err: err}
			continue
		}

		var value []byte
		for _, tx := range resp.Result {
			if tx.Nout != nout {
				continue
			}

			value, err = hex.DecodeString(tx.Value)
			break
		}
		if err != nil {
			values[outpoint] = valOrErr{Err: err}
			continue
		}

		claim := &types.Claim{}
		err = proto.Unmarshal(value, claim)
		if err != nil {
			values[outpoint] = valOrErr{Err: err}
			continue
		}

		if claim.GetStream().GetSource().GetSourceType() != types.Source_lbry_sd_hash {
			values[outpoint] = valOrErr{Err: errors.Err("source is nil or source type is not lbry_sd_hash")}
			continue
		}

		values[outpoint] = valOrErr{Value: hex.EncodeToString(claim.GetStream().GetSource().GetSource())}
	}

	return values, nil
}
