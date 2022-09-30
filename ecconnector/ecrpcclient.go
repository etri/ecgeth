package ecconnector

//rpc client provides kind of library API for calling rest APIs of external consensus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ecpon"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

const (
	NewHeadPath        = "/newhead"
	VerifySealPath     = "/verifyseal"
	KeepAlivePath      = "/heartbeat"
	BuildBlkResultPath = "/buildblkresult"
)

//defines External Consensus Error
type EcError struct {
	Code string // error code
	Msg  string // error string
}

type cacheSendVerifySeal struct {
	blkno  uint64
	bhash  string
	bproof []byte
}

type BuildBlkRstParam struct {
	PBlockno  uint64 `json:"pblockno"` //parent block no
	NBlockno  uint64 `json:"nblockno"` //new block no
	NBHash    string `json:"nbhash"`   //new block hash
	ErrReason string `json:"errreason"`
}

type BlockParam struct {
	Blockno uint64            `json:"blockno"`
	BHash   string            `json:"bhash"`
	BProof  *ecpon.BlockProof `json:"blockproof"`
}

type RpcClient struct {
	block *types.Block

	blockno  uint64 //block id cached
	bhash    string //block hash caced
	cachesvs *cacheSendVerifySeal

	url    string
	client *http.Client
}

func NewRpcClient(url string) *RpcClient {

	rpc := &RpcClient{
		url: url,
	}

	//rpc.client = &http.Client{}

	//time out could be used.
	rpc.client = &http.Client{Timeout: time.Duration(30) * time.Second}

	return rpc
}

//Send KeepAlive, it included cached information
func (r *RpcClient) SendKeepAlive() error {

	log.Trace("Sending Keep Alive")

	jsonBytes, err := json.Marshal(r.blockno)

	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, r.url+KeepAlivePath, bytes.NewBuffer(jsonBytes))

	if err != nil {
		log.Debug("Failed to make a request to external consensus")
		return err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := r.client.Do(req)

	if err != nil {
		log.Warn("Failed to send request to external consensus", "err", err)
		return err
	}

	log.Trace("KeepAlive RPC Call: Response", "status", resp.Status)

	//if you want to reuse http connection, you must reall all response body and close it
	defer resp.Body.Close()

	// if status is ok
	if resp.StatusCode == 200 || resp.StatusCode == 201 || resp.StatusCode == 204 {
		return nil
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Debug("reading response, there is an error", "err", err)
		return errors.New(resp.Status + ":" + "can not read body")
	}

	var ecerr EcError
	//get External Consensus Error
	uerr := json.Unmarshal(body, &ecerr)
	if uerr != nil {
		return errors.New(resp.Status + ":" + "can not unmarshal body")
	}

	log.Warn("Sending Keep Alive Error", "error code", ecerr.Code)
	return errors.New(resp.Status + ":" + ecerr.Code)
}

//Send result of buildBlock request
//rblkno: requested blockno which is parent blk, blocno and bhash is for new block
func (r *RpcClient) SendBuildBlkResult(rblkno uint64, blockno uint64, bhash common.Hash, berr error) error {

	r.blockno = blockno
	r.bhash = bhash.Hex()

	log.Debug(fmt.Sprintf("Send BuildBlkResult rpc request for block:%d %d %s", rblkno, blockno, r.bhash))

	jsonBytes, err := json.Marshal(&BuildBlkRstParam{rblkno, r.blockno, r.bhash, ""})

	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, r.url+BuildBlkResultPath, bytes.NewBuffer(jsonBytes))

	if err != nil {
		log.Debug("Failed to make a request to external consensus")
		return err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := r.client.Do(req)

	if err != nil {
		log.Warn("Failed to send request to external consensus", "err", err)
		return err
	}

	log.Info("SendBuildBlkResult Rpc Call Response", "status", resp.Status)

	//if you want to reuse http connection, you must reall all response body and close it
	defer resp.Body.Close()

	// if status is ok
	if resp.StatusCode == 200 || resp.StatusCode == 201 || resp.StatusCode == 204 {
		return nil
	}

	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Debug("reading response, there is an error", "err", err)
		return errors.New(resp.Status + ":" + "can not read body")
	}
	//don't need to read response body, it does not contain any body
	return errors.New(resp.Status)

}

//Send NewHeadEv request (a block hash)
func (r *RpcClient) SendNewHead(blockno uint64, bhash common.Hash, bproof *ecpon.BlockProof) error {

	r.blockno = blockno
	//	r.content=base64.StdEncoding.EncodeToString(bhash.Bytes())
	r.bhash = bhash.Hex()

	log.Debug(fmt.Sprintf("Send NewHead rpc request for block:%d", r.blockno))

	jsonBytes, err := json.Marshal(&BlockParam{r.blockno, r.bhash, bproof})

	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, r.url+NewHeadPath, bytes.NewBuffer(jsonBytes))

	if err != nil {
		log.Debug("Failed to make a request to external consensus")
		return err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := r.client.Do(req)

	if err != nil {
		log.Warn("Failed to send request to external consensus", "err", err)
		return err
	}

	log.Info("Send NewHead Rpc Call Response", "status", resp.Status)

	//if you want to reuse http connection, you must reall all response body and close it
	defer resp.Body.Close()

	// if status is ok
	if resp.StatusCode == 200 || resp.StatusCode == 201 || resp.StatusCode == 204 {
		return nil
	}

	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Debug("reading response, there is an error", "err", err)
		return errors.New(resp.Status + ":" + "can not read body")
	}
	//don't need to read response body, it does not contain any body
	return errors.New(resp.Status)

}

//Send VerifySeal request (a block hash)
func (r *RpcClient) SendVerifySeal(blockno uint64, bhash common.Hash, bproof *ecpon.BlockProof) error {

	r.blockno = blockno
	//	r.content=base64.StdEncoding.EncodeToString(bhash.Bytes())
	r.bhash = bhash.Hex()

	log.Debug(fmt.Sprintf("Send VerifySeal rpc request for block:%d", r.blockno))

	jsonBytes, err := json.Marshal(&BlockParam{r.blockno, r.bhash, bproof})

	if r.cachesvs != nil {
		if (r.cachesvs.blkno == blockno) && (strings.Compare(r.cachesvs.bhash, bhash.Hex()) == 0) &&
			(bytes.Equal(r.cachesvs.bproof, jsonBytes)) {
			log.Debug("retuning cached result of SendVerfiySeal")
			return nil
		}
	}

	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, r.url+VerifySealPath, bytes.NewBuffer(jsonBytes))

	if err != nil {
		log.Debug("Failed to make a request to external consensus")
		return err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := r.client.Do(req)

	if err != nil {
		log.Warn("Failed to send request to external consensus", "err", err)
		return err
	}

	log.Info("VerifySeal Rpc Call Response", "status", resp.Status)

	//if you want to reuse http connection, you must reall all response body and close it
	defer resp.Body.Close()

	// if status is ok
	if resp.StatusCode == 200 || resp.StatusCode == 201 || resp.StatusCode == 204 {
		//caching last successful result of SendVerifySeal
		r.cachesvs = &cacheSendVerifySeal{blkno: blockno, bhash: bhash.Hex(), bproof: jsonBytes}
		return nil
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Debug("reading response, there is an error", "err", err)
		return errors.New(resp.Status + ":" + "can not read body")
	}

	var ecerr EcError
	//get External Consensus Error
	uerr := json.Unmarshal(body, &ecerr)
	if uerr != nil {
		return errors.New(resp.Status + ":" + "can not unmarshal body")
	}

	log.Debug("External Consensus Error", "error code", ecerr.Code)
	return errors.New(resp.Status + ":" + ecerr.Code)

}
