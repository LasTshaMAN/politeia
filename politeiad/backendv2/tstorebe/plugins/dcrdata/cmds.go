// Copyright (c) 2020-2021 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package dcrdata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	jsonrpc "github.com/decred/dcrd/rpc/jsonrpc/types/v2"
	v5 "github.com/decred/dcrdata/api/types/v5"
	"github.com/decred/politeia/politeiad/plugins/dcrdata"
	"github.com/decred/politeia/util"
)

// cmdBestBlock returns the best block. If the dcrdata websocket has been
// disconnected the best block will be fetched from the dcrdata HTTP API. If
// dcrdata cannot be reached then the most recent cached best block will be
// returned along with a status of StatusDisconnected. It is the callers
// responsibility to determine if the stale best block should be used.
func (p *dcrdataPlugin) cmdBestBlock(payload string) (string, error) {
	// Payload is empty. Nothing to decode.

	// Get the cached best block
	bb := p.bestBlockGet()
	var (
		fetch  bool
		stale  uint32
		status = dcrdata.StatusConnected
	)
	switch {
	case bb == 0:
		// No cached best block means that the best block has not been
		// populated by the websocket yet. Fetch is manually.
		fetch = true
	case p.bestBlockIsStale():
		// The cached best block has been populated by the websocket, but
		// the websocket is currently disconnected and the cached value
		// is stale. Try to fetch the best block manually and only use
		// the stale value if manually fetching it fails.
		fetch = true
		stale = bb
	}

	// Fetch the best block manually if required
	if fetch {
		block, err := p.bestBlockHTTP()
		switch {
		case err == nil:
			// We got the best block. Use it.
			bb = block.Height
		case stale != 0:
			// Unable to fetch the best block manually. Use the stale
			// value and mark the connection status as disconnected.
			bb = stale
			status = dcrdata.StatusDisconnected
		default:
			// Unable to fetch the best block manually and there is no
			// stale cached value to return.
			return "", fmt.Errorf("bestBlockHTTP: %v", err)
		}
	}

	// Prepare reply
	bbr := dcrdata.BestBlockReply{
		Status: status,
		Height: bb,
	}
	reply, err := json.Marshal(bbr)
	if err != nil {
		return "", err
	}

	return string(reply), nil
}

// cmdBlockDetails retrieves the block details for the provided block height.
func (p *dcrdataPlugin) cmdBlockDetails(payload string) (string, error) {
	// Decode payload
	var bd dcrdata.BlockDetails
	err := json.Unmarshal([]byte(payload), &bd)
	if err != nil {
		return "", err
	}

	// Fetch block details
	bdb, err := p.blockDetails(bd.Height)
	if err != nil {
		return "", fmt.Errorf("blockDetails: %v", err)
	}

	// Prepare reply
	bdr := dcrdata.BlockDetailsReply{
		Block: convertBlockDataBasicFromV5(*bdb),
	}
	reply, err := json.Marshal(bdr)
	if err != nil {
		return "", err
	}

	return string(reply), nil
}

// cmdTicketPool requests the lists of tickets in the ticket pool at a
// specified block hash.
func (p *dcrdataPlugin) cmdTicketPool(payload string) (string, error) {
	// Decode payload
	var tp dcrdata.TicketPool
	err := json.Unmarshal([]byte(payload), &tp)
	if err != nil {
		return "", err
	}

	// Get the ticket pool
	tickets, err := p.ticketPool(tp.BlockHash)
	if err != nil {
		return "", fmt.Errorf("ticketPool: %v", err)
	}

	// Prepare reply
	tpr := dcrdata.TicketPoolReply{
		Tickets: tickets,
	}
	reply, err := json.Marshal(tpr)
	if err != nil {
		return "", err
	}

	return string(reply), nil
}

// TxsTrimmed requests the trimmed transaction information for the provided
// transaction IDs.
func (p *dcrdataPlugin) cmdTxsTrimmed(payload string) (string, error) {
	// Decode payload
	var tt dcrdata.TxsTrimmed
	err := json.Unmarshal([]byte(payload), &tt)
	if err != nil {
		return "", err
	}

	// Get trimmed txs
	txs, err := p.txsTrimmed(tt.TxIDs)
	if err != nil {
		return "", fmt.Errorf("txsTrimmed: %v", err)
	}

	// Prepare reply
	ttr := dcrdata.TxsTrimmedReply{
		Txs: convertTrimmedTxsFromV5(txs),
	}
	reply, err := json.Marshal(ttr)
	if err != nil {
		return "", err
	}

	return string(reply), nil
}

// makeReq makes a dcrdata http request to the method and route provided,
// serializing the provided object as the request body, and returning a byte
// slice of the response body. An error is returned if dcrdata responds with
// anything other than a 200 http status code.
func (p *dcrdataPlugin) makeReq(method string, route string, headers map[string]string, v interface{}) ([]byte, error) {
	var (
		url     = p.hostHTTP + route
		reqBody []byte
		err     error
	)

	log.Tracef("%v %v", method, url)

	// Setup request
	if v != nil {
		reqBody, err = json.Marshal(v)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Add(k, v)
	}

	// Send request
	r, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()

	// Handle response
	if r.StatusCode != http.StatusOK {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("%v %v %v %v",
				r.StatusCode, method, url, err)
		}
		return nil, fmt.Errorf("%v %v %v %s",
			r.StatusCode, method, url, body)
	}

	return util.RespBody(r), nil
}

// bestBlockHTTP fetches and returns the best block from the dcrdata http API.
func (p *dcrdataPlugin) bestBlockHTTP() (*v5.BlockDataBasic, error) {
	resBody, err := p.makeReq(http.MethodGet, routeBestBlock, nil, nil)
	if err != nil {
		return nil, err
	}

	var bdb v5.BlockDataBasic
	err = json.Unmarshal(resBody, &bdb)
	if err != nil {
		return nil, err
	}

	return &bdb, nil
}

// blockDetails returns the block details for the block at the specified block
// height.
func (p *dcrdataPlugin) blockDetails(height uint32) (*v5.BlockDataBasic, error) {
	h := strconv.FormatUint(uint64(height), 10)

	route := strings.Replace(routeBlockDetails, "{height}", h, 1)
	resBody, err := p.makeReq(http.MethodGet, route, nil, nil)
	if err != nil {
		return nil, err
	}

	var bdb v5.BlockDataBasic
	err = json.Unmarshal(resBody, &bdb)
	if err != nil {
		return nil, err
	}

	return &bdb, nil
}

// ticketPool returns the list of tickets in the ticket pool at the specified
// block hash.
func (p *dcrdataPlugin) ticketPool(blockHash string) ([]string, error) {
	route := strings.Replace(routeTicketPool, "{hash}", blockHash, 1)
	route += "?sort=true"
	resBody, err := p.makeReq(http.MethodGet, route, nil, nil)
	if err != nil {
		return nil, err
	}

	var tickets []string
	err = json.Unmarshal(resBody, &tickets)
	if err != nil {
		return nil, err
	}

	return tickets, nil
}

// txsTrimmed returns the TrimmedTx for the specified tx IDs.
func (p *dcrdataPlugin) txsTrimmed(txIDs []string) ([]v5.TrimmedTx, error) {
	t := v5.Txns{
		Transactions: txIDs,
	}
	headers := map[string]string{
		headerContentType: contentTypeJSON,
	}
	resBody, err := p.makeReq(http.MethodPost, routeTxsTrimmed, headers, t)
	if err != nil {
		return nil, err
	}

	var txs []v5.TrimmedTx
	err = json.Unmarshal(resBody, &txs)
	if err != nil {
		return nil, err
	}

	return txs, nil
}

func convertTicketPoolInfoFromV5(t v5.TicketPoolInfo) dcrdata.TicketPoolInfo {
	return dcrdata.TicketPoolInfo{
		Height:  t.Height,
		Size:    t.Size,
		Value:   t.Value,
		ValAvg:  t.ValAvg,
		Winners: t.Winners,
	}
}

func convertBlockDataBasicFromV5(b v5.BlockDataBasic) dcrdata.BlockDataBasic {
	var poolInfo *dcrdata.TicketPoolInfo
	if b.PoolInfo != nil {
		p := convertTicketPoolInfoFromV5(*b.PoolInfo)
		poolInfo = &p
	}
	return dcrdata.BlockDataBasic{
		Height:     b.Height,
		Size:       b.Size,
		Hash:       b.Hash,
		Difficulty: b.Difficulty,
		StakeDiff:  b.StakeDiff,
		Time:       b.Time.UNIX(),
		NumTx:      b.NumTx,
		MiningFee:  b.MiningFee,
		TotalSent:  b.TotalSent,
		PoolInfo:   poolInfo,
	}
}

func convertScriptSigFromJSONRPC(s jsonrpc.ScriptSig) dcrdata.ScriptSig {
	return dcrdata.ScriptSig{
		Asm: s.Asm,
		Hex: s.Hex,
	}
}

func convertVinFromJSONRPC(v jsonrpc.Vin) dcrdata.Vin {
	var scriptSig *dcrdata.ScriptSig
	if v.ScriptSig != nil {
		s := convertScriptSigFromJSONRPC(*v.ScriptSig)
		scriptSig = &s
	}
	return dcrdata.Vin{
		Coinbase:    v.Coinbase,
		Stakebase:   v.Stakebase,
		Txid:        v.Txid,
		Vout:        v.Vout,
		Tree:        v.Tree,
		Sequence:    v.Sequence,
		AmountIn:    v.AmountIn,
		BlockHeight: v.BlockHeight,
		BlockIndex:  v.BlockIndex,
		ScriptSig:   scriptSig,
	}
}

func convertVinsFromV5(ins []jsonrpc.Vin) []dcrdata.Vin {
	i := make([]dcrdata.Vin, 0, len(ins))
	for _, v := range ins {
		i = append(i, convertVinFromJSONRPC(v))
	}
	return i
}

func convertScriptPubKeyFromV5(s v5.ScriptPubKey) dcrdata.ScriptPubKey {
	return dcrdata.ScriptPubKey{
		Asm:       s.Asm,
		Hex:       s.Hex,
		ReqSigs:   s.ReqSigs,
		Type:      s.Type,
		Addresses: s.Addresses,
		CommitAmt: s.CommitAmt,
	}
}

func convertTxInputIDFromV5(t v5.TxInputID) dcrdata.TxInputID {
	return dcrdata.TxInputID{
		Hash:  t.Hash,
		Index: t.Index,
	}
}

func convertVoutFromV5(v v5.Vout) dcrdata.Vout {
	var spend *dcrdata.TxInputID
	if v.Spend != nil {
		s := convertTxInputIDFromV5(*v.Spend)
		spend = &s
	}
	return dcrdata.Vout{
		Value:               v.Value,
		N:                   v.N,
		Version:             v.Version,
		ScriptPubKeyDecoded: convertScriptPubKeyFromV5(v.ScriptPubKeyDecoded),
		Spend:               spend,
	}
}

func convertVoutsFromV5(outs []v5.Vout) []dcrdata.Vout {
	o := make([]dcrdata.Vout, 0, len(outs))
	for _, v := range outs {
		o = append(o, convertVoutFromV5(v))
	}
	return o
}

func convertTrimmedTxFromV5(t v5.TrimmedTx) dcrdata.TrimmedTx {
	return dcrdata.TrimmedTx{
		TxID:     t.TxID,
		Version:  t.Version,
		Locktime: t.Locktime,
		Expiry:   t.Expiry,
		Vin:      convertVinsFromV5(t.Vin),
		Vout:     convertVoutsFromV5(t.Vout),
	}
}

func convertTrimmedTxsFromV5(txs []v5.TrimmedTx) []dcrdata.TrimmedTx {
	t := make([]dcrdata.TrimmedTx, 0, len(txs))
	for _, v := range txs {
		t = append(t, convertTrimmedTxFromV5(v))
	}
	return t
}
