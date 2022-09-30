package ecconnector

//implements External Consensus Connector which is coworking with pon/bada instance

import (
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ecpon"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/downloader"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

// Backend wraps all methods required for mining.
type Backend interface {
	BlockChain() *core.BlockChain
	TxPool() *core.TxPool
	BroadcastBlockToAll(block *types.Block)
	SendNewBlock(pidList []string, block *types.Block, td *big.Int)
}

//holding information about unsend new head
type unconsumedNewHead struct {
	blkno  uint64
	bhash  common.Hash
	bproof *ecpon.BlockProof
}

// newBlockMinedReq represents a request in order to notity new block batching work has been finished
type newBlockMinedReq struct {
	err error
	blk *types.Block
}

type EcConnector struct {
	chainConfig *params.ChainConfig
	eth         Backend
	engine      consensus.Engine
	coinbase    common.Address
	mux         *event.TypeMux
	rpcclient   *RpcClient
	batcher     *EcBatcher
	//cblock      map[common.Hash]*types.Block
	cblock *types.Block
	rBlkNo uint64 //requested block number when buildBlock API called

	// Channels
	stopCh          chan struct{}
	newBlockMinedCh chan *newBlockMinedReq

	newHeadCh  chan core.ChainHeadEvent //channel to receive new block event from blockchain
	newHeadSub event.Subscription       //new block event subscription

	wg sync.WaitGroup
}

// New creates a new External Consensus Connector
func New(eth Backend, config *Config, chainConfig *params.ChainConfig, engine consensus.Engine, mux *event.TypeMux) *EcConnector {
	ecconnector := &EcConnector{
		eth:             eth,
		chainConfig:     chainConfig,
		engine:          engine,
		stopCh:          make(chan struct{}),
		newBlockMinedCh: make(chan *newBlockMinedReq),
		newHeadCh:       make(chan core.ChainHeadEvent, 10),
		mux:             mux,
	}

	//set the default coinbase
	ecconnector.coinbase = common.Address{}
	ecconnector.rpcclient = NewRpcClient(config.EcUrl)
	ecconnector.batcher = NewEcBatcher(eth, config, chainConfig, engine, mux, ecconnector.NotifyMinedBlock)

	return ecconnector
}

func (ec *EcConnector) Start(coinbase common.Address) {

	log.Info("Starting ec connector")
	ec.coinbase = coinbase
	ec.newHeadSub = ec.eth.BlockChain().SubscribeChainHeadEvent(ec.newHeadCh)

	//starting  Batcher
	ec.batcher.Start()

	// run loop for processing rpc pon connector
	go ec.loop()

}

func (ec *EcConnector) Stop() {
	//closing stopCh in order for mainloop to get stop signal
	close(ec.stopCh)

	//waiting for main loop to be terminated
	ec.wg.Wait()

	log.Trace("getting out of ecconnector loop")
}

//batcher will call this API if mining block finished
func (ec *EcConnector) NotifyMinedBlock(err error, blk *types.Block) {
	ec.newBlockMinedCh <- &newBlockMinedReq{err: err, blk: blk}
}

func (ec *EcConnector) GetEcRpcHandler() ecpon.EcRpcCall {
	return ec.rpcclient
}

func (ec *EcConnector) Mining() bool {
	return ec.batcher.isRunning()
}

func (ec *EcConnector) SetEtherbase(addr common.Address) {
	ec.coinbase = addr
	ec.batcher.setEtherbase(addr)
}

// this is the main loop of ecconnector
func (ec *EcConnector) loop() {

	ec.wg.Add(1)
	defer ec.wg.Done()
	defer ec.newHeadSub.Unsubscribe()

	//this time ticker is used for sending heartbeat
	hbTimer := time.NewTicker(30 * time.Second)
	defer hbTimer.Stop()

	//sending the first HB
	ec.rpcclient.SendKeepAlive()

	//this time ticker is used for sending unsent NewHead
	ucnewheadTimer := time.NewTicker(2 * time.Second)
	defer ucnewheadTimer.Stop()
	//unsendNewHeadQueue includes NewHead event was not delivered to external consensus
	//It is going to be OK just to keep track of the lastest new block
	//so single variable is ok
	var usnewhead *unconsumedNewHead

	events := ec.mux.Subscribe(downloader.StartEvent{}, downloader.DoneEvent{}, downloader.FailedEvent{})
	defer func() {
		if !events.Closed() {
			events.Unsubscribe()
		}
	}()

	// we don't need canStart, shoudStart flag because the mining can not be turned off
	//shouldStart := false
	//canStart := true
	dlEventCh := events.Chan()
	//main loop of ecconnector
	for {
		select {
		case <-hbTimer.C:
			//sending HB
			ec.rpcclient.SendKeepAlive()
		case <-ucnewheadTimer.C:
			//sending the latest unsent new header event
			if usnewhead != nil {
				log.Trace("try to resend unconsumed new head", "blkno", usnewhead.blkno, "blockhash", usnewhead.bhash)
				err := ec.rpcclient.SendNewHead(usnewhead.blkno, usnewhead.bhash, usnewhead.bproof)
				if err == nil {
					usnewhead = nil
				}
			} else {
				log.Trace("there is no unconsumed new head")
			}
		case nbReq := <-ec.newBlockMinedCh: //if block is mined
			if nbReq.err != nil {
				log.Debug("newBlockMined Error", "err", nbReq.err)
				//if there is error, set blkno=0, hash=nil, error=err
				ec.rpcclient.SendBuildBlkResult(ec.rBlkNo, 0, common.Hash{}, nbReq.err)
			} else {
				log.Debug("newBlockMined successfully", "new block no", nbReq.blk.NumberU64(), "bhash", nbReq.blk.Hash().Hex())
				ec.cblock = nbReq.blk
				ec.rpcclient.SendBuildBlkResult(ec.rBlkNo, nbReq.blk.NumberU64(), nbReq.blk.Hash(), nbReq.err)
			}
			//even though there is error, do not need to notity the error to Batcher
			//becasue batcher can do new batching with timeout

		case ev := <-ec.newHeadCh: //new block was inserted successfully

			block := ev.Block
			log.Info(fmt.Sprintf("New Block:%d", block.NumberU64()))

			_, bproof, err := ecpon.ReadExtra(block.Header())

			if err == nil {
				//sending NewHead Event
				rerr := ec.rpcclient.SendNewHead(block.NumberU64(), block.Hash(), bproof)
				if rerr != nil {
					log.Debug("NewHead event is not consumed by external consensus", "reason", rerr)
					newhead := &unconsumedNewHead{blkno: block.NumberU64(), bhash: block.Hash(), bproof: bproof}
					usnewhead = newhead
				} else {
					usnewhead = nil
				}
			} else {
				log.Warn("Reading extradata failed")
			}

		case ev := <-dlEventCh:
			if ev == nil {
				// Unsubscription done, stop listening
				dlEventCh = nil
				continue
			}
			switch ev.Data.(type) {
			case downloader.StartEvent:
				wasMining := ec.Mining()
				ec.batcher.stopBatching()

				if wasMining {
					log.Debug("Mining aborted due to sync")
				}
			case downloader.FailedEvent:
				ec.SetEtherbase(ec.coinbase)
				ec.batcher.startBatching()
			case downloader.DoneEvent:
				ec.SetEtherbase(ec.coinbase)
				ec.batcher.startBatching()
				// Stop reacting to downloader events
				events.Unsubscribe()
			}

		case <-ec.newHeadSub.Err():
			ec.batcher.Stop()
			return
		case <-ec.stopCh:
			ec.batcher.Stop()
			return
		}
	}

}

// APIs for EcConnector
//this time not used, rpc APIs are directly implemented in eth
/*
func (ec *EcConnector) APIs(chain consensus.ChainReader) []rpc.API {
	return []rpc.API{{
		Namespace: "ecconnector",
		Version:   "1.0",
		Service:   &API{chain: chain, ec: ec},
		Public:    true,
	}}
}
*/
