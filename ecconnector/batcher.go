//implement the tx batcher, produces kind of cadidate block, and seals the block

package ecconnector

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ecpon"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
)

const (
	// txChanSize is the size of channel listening to NewTxsEvent.
	// The number is referenced from the size of tx pool.
	txChanSize = 4096
	// chainHeadChanSize is the size of channel listening to ChainHeadEvent.
	chainHeadChanSize = 10

	// chainSideChanSize is the size of channel listening to ChainSideEvent.
	chainSideChanSize = 10

	// recommitInterval in sec
	//even though there is no newhead, new bataching work started after this interval
	recommitInterval = 5

	// maxMessageSize is the maximum cap on the size of a protocol message.
	maxReadyTxs = 20 * 1024
	GasCeil     = 0x8000000
	GasFloor    = 0x8000000
)

var (
	EcGethDummyExtra []byte = []byte("EcGeth Extra")
)

// task contains all information for consensus engine sealing and result submitting.
type task struct {
	receipts  []*types.Receipt
	state     *state.StateDB
	block     *types.Block
	createdAt time.Time
}

// environment is the batcher's current environment and holds all of the current state information.
type environment struct {
	signer types.Signer

	state   *state.StateDB // apply state changes here
	tcount  int            // tx count in cycle
	gasPool *core.GasPool  // available gas used to pack transactions

	header       *types.Header
	txs          []*types.Transaction               //tx executed on the current state
	receipts     []*types.Receipt                   // receipt of executed tx
	processedTxs map[common.Hash]*types.Transaction //by yim, for seaching the tx which is already processed
	//for testing whether tx event processing is meaningful
	fTcount_s int // first suggested tx count by newwork
	fTcount_c int // first executed tx count by newwork

	pTcount_s int //number of pending tx of tx pool
	pTcount_n int // new tx number among second tx pool

	errGasLcnt         int
	errNonceLcnt       int
	errNonceHcnt       int
	errNotSupportedcnt int
	errDefaultcnt      int
	//end1

	tstart time.Time
}

//batcher create a block and seals the block
func NewEcBatcher(eth Backend, config *Config, chainConfig *params.ChainConfig, engine consensus.Engine, mux *event.TypeMux, notifyMinedBlock func(err error, block *types.Block)) *EcBatcher {
	batcher := &EcBatcher{
		eth:         eth,
		engine:      engine,
		config:      config,
		chainconfig: chainConfig,
		mux:         mux,
		startCh:     make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
		//	ecCommitCh:  make(chan struct{}, 1),
		newWorkCh:   make(chan *newWorkReq),
		txsCh:       make(chan core.NewTxsEvent, txChanSize),
		chainHeadCh: make(chan core.ChainHeadEvent, chainHeadChanSize),
		//	chainSideCh: make(chan core.ChainSideEvent, chainSideChanSize),
		notifyMinedBlock: notifyMinedBlock,
	}

	//set the default coinbase
	batcher.coinbase = common.Address{}
	// Subscribe NewTxsEvent for tx pool
	//batcher.txsSub = eth.TxPool().SubscribeNewTxsEvent(batcher.txsCh)
	// Subscribe events for blockchain
	batcher.chainHeadSub = eth.BlockChain().SubscribeChainHeadEvent(batcher.chainHeadCh)
	//batcher.chainSideSub = eth.BlockChain().SubscribeChainSideEvent(batcher.chainSideCh)

	// Sanitize recommit interval if the user-specified one is too short.
	recommit := time.Duration(recommitInterval * time.Second)

	go batcher.mainLoop()
	go batcher.newWorkLoop(recommit)

	return batcher
}

const (
	commitInterruptNone int32 = iota
	commitInterruptNewHead
	commitInterruptResubmit
)

// newWorkReq represents a request for new block batching work submitting with relative interrupt notifier.
type newWorkReq struct {
	interrupt *int32
	noempty   bool //indicating whether newwork has empty set of tx, but it is not used by ecgeth
	timestamp int64
}

type EcBatcher struct {
	eth         Backend
	config      *Config
	chainconfig *params.ChainConfig
	engine      consensus.Engine

	currentenv  *environment
	pendingTask *task //we have only one pendingTask

	// Feeds, I don't understand how this feed works, yim
	pendingLogsFeed event.Feed

	coinbase common.Address

	mu           sync.RWMutex // The lock used to protect the coinbase and extra fields
	buildblockMu sync.RWMutex //the lock used to protect buildblock api call

	snapshotMu    sync.RWMutex // The lock used to protect the block snapshot and state snapshot
	snapshotBlock *types.Block
	snapshotState *state.StateDB

	// atomic status counters
	running int32 // The indicator whether the consensus engine is running or not.
	newTxs  int32 // New arrival transaction count since last batching work submitting.

	isBuildBlockProcessing bool // this indicate whether BuildBlock is processing or not

	//I am not sure, the thread will be used or not
	//if threads used, following channel is used for mananging lifecycle
	startCh    chan struct{}
	stopCh     chan struct{}
	ecCommitCh chan struct{} //for informing to do commit by external consensus
	newWorkCh  chan *newWorkReq

	mux          *event.TypeMux
	txsCh        chan core.NewTxsEvent
	txsSub       event.Subscription
	chainHeadCh  chan core.ChainHeadEvent
	chainHeadSub event.Subscription
	//chainSideCh  chan core.ChainSideEvent
	//chainSideSub event.Subscription

	notifyMinedBlock func(err error, block *types.Block) // call when the the block is ready to inform ecconnector
}

func (b *EcBatcher) Start() {
	log.Info("starting the batcher")

	//batcher is always running. so starting bathcer
	b.startBatching()

}

//stopping batcher itself
func (b *EcBatcher) Stop() {
	log.Info("stopping the batcher")

	if b.currentenv != nil && b.currentenv.state != nil {
		b.currentenv.state.StopPrefetcher()
	}

	atomic.StoreInt32(&b.running, 0)
	close(b.stopCh)
}

// start sets the running status as 1 and triggers new work submitting.
func (b *EcBatcher) startBatching() {
	atomic.StoreInt32(&b.running, 1)
	b.startCh <- struct{}{}
}

// stop sets the running status as 0.
func (b *EcBatcher) stopBatching() {
	atomic.StoreInt32(&b.running, 0)
}

// setEtherbase sets the etherbase used to initialize the block coinbase field.
func (b *EcBatcher) setEtherbase(addr common.Address) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.coinbase = addr
}

// isRunning returns an indicator whether worker is running or not.
func (b *EcBatcher) isRunning() bool {
	return atomic.LoadInt32(&b.running) == 1
}

// newWorkLoop is a standalone goroutine to submit new mining work upon received events.
func (b *EcBatcher) newWorkLoop(recommit time.Duration) {
	var (
		interrupt *int32
		timestamp int64 // timestamp for each round of mining.
	)

	timer := time.NewTimer(0)
	defer timer.Stop()
	<-timer.C // discard the initial tick

	// commit aborts in-flight transaction execution with given signal and resubmits a new one.
	commit := func(noempty bool, s int32) {
		if interrupt != nil {
			atomic.StoreInt32(interrupt, s)
		}
		interrupt = new(int32)
		select {
		case b.newWorkCh <- &newWorkReq{interrupt: interrupt, noempty: noempty, timestamp: timestamp}:
		case <-b.stopCh:
			return
		}
		timer.Reset(recommit)
		atomic.StoreInt32(&b.newTxs, 0)
	}

	for {
		select {
		case <-b.startCh:
			if !b.isBuildBlockProcessing {
				b.clearPending()
				timestamp = time.Now().Unix()
				commit(false, commitInterruptNewHead)
			}

		case head := <-b.chainHeadCh:
			log.Debug("New Head ev, start new batching", "blk no", head.Block.NumberU64())
			//let's clear previous snapshot
			b.clearPending()
			timestamp = time.Now().Unix()
			commit(false, commitInterruptNewHead)

		case <-timer.C:
			// If mining is running resubmit a new work cycle periodically to pull in
			// higher priced transactions. Disable this overhead for pending blocks.
			if b.isRunning() {
				// Short circuit if no new transaction arrives.
				if atomic.LoadInt32(&b.newTxs) == 0 {
					timer.Reset(recommit)
					continue
				}
				commit(true, commitInterruptResubmit)
			}

		case <-b.stopCh:
			return
		}
	}
}

// mainLoop is a standalone goroutine to regenerate the sealing task based on the received event.
func (b *EcBatcher) mainLoop() {
	//defer b.txsSub.Unsubscribe()
	defer b.chainHeadSub.Unsubscribe()
	//defer b.chainSideSub.Unsubscribe()

	for {
		select {
		case req := <-b.newWorkCh:
			//making a new pending block based on tx of tx pool
			b.commitNewWork(req.interrupt, req.noempty, req.timestamp)
			b.currentenv.fTcount_c = b.currentenv.tcount
			b.currentenv.tstart = time.Now() //for estimating time consumed from newwork to commit

			//let's update snapshot
			b.updateSnapshot()
			/* not used.
			case ev := <-b.txsCh:
				// Apply transactions to the pending state
				//
				// Note all transactions received may not be continuous with transactions
				// already included in the current mining block. These transactions will
				// be automatically eliminated.
				//if !b.isRunning() && b.currentenv != nil {
				if b.currentenv != nil {
					// If block is already full, abort
					if gp := b.currentenv.gasPool; gp != nil && gp.Gas() < params.TxGas {
						continue
					}
					b.mu.RLock()
					coinbase := b.coinbase
					b.mu.RUnlock()

					txs := make(map[common.Address]types.Transactions)
					for _, tx := range ev.Txs {
						b.currentenv.nTcount_a++

						//let's check the tx whether it is already processed or not, by yim
						_, ok := b.currentenv.processedTxs[tx.Hash()]
						if !ok {
							acc, _ := types.Sender(b.currentenv.signer, tx)
							txs[acc] = append(txs[acc], tx)
							log.Info("new tx arrived", "hash", tx.Hash())
							b.currentenv.nTcount_n++
						} else {
							log.Info("old tx arrived", "hash", tx.Hash())
						}
					}
					txset := types.NewTransactionsByPriceAndNonce(b.currentenv.signer, txs)
					tcount := b.currentenv.tcount
					b.commitTransactions(txset, coinbase, nil)
					// Only update the snapshot if any new transactons were added
					// to the pending block
					if tcount != b.currentenv.tcount {
						b.updateSnapshot()
					}
					log.Info("new tx commited", "txnum", tcount)
				}
				atomic.AddInt32(&b.newTxs, int32(len(ev.Txs)))
			*/
		// System stopped
		case <-b.stopCh:
			return
			/*	case <-b.txsSub.Err():
				return
			*/
		case <-b.chainHeadSub.Err():
			return
			//case <-b.chainSideSub.Err():
			//	return
		}
	}
}

func (b *EcBatcher) SealBlock(blk *types.Block, blockProof *ecpon.BlockProof, nextcommittee *ecpon.ConsensusGroup) (*types.Block, error) {

	chain := b.eth.BlockChain()

	ecPonBada, ok := b.engine.(*ecpon.EcPonBada)

	if !ok {
		return nil, errors.New("EC Pon/BADA Engine can not be found")
	}

	sblk, err := ecPonBada.SealEcPonBada(chain, blk, blockProof, nextcommittee)

	if err != nil {
		log.Info("block sealing failed", err)
		return nil, err
	}

	return sblk, nil
}

// makeCurrent creates a new environment for the current cycle.
func (b *EcBatcher) makeCurrent(parent *types.Block, header *types.Header) error {

	chain := b.eth.BlockChain()
	// Retrieve the parent state to execute on top and start a prefetcher for
	// the miner to speed block sealing up a bit
	state, err := chain.StateAt(parent.Root())
	if err != nil {
		return err
	}
	state.StartPrefetcher("miner")

	env := &environment{
		//we're going to use single type of singer which is the latest
		signer: types.LatestSigner(chain.Config()),
		state:  state,
		header: header,
		/* not used by ec geth
		ancestors: mapset.NewSet(),
		family:    mapset.NewSet(),
		uncles:    mapset.NewSet(),
		*/
		processedTxs: make(map[common.Hash]*types.Transaction),
	}

	env.tcount = 0

	// Swap out the old work with the new one, terminating any leftover prefetcher
	// processes in the mean time and starting a new one.
	if b.currentenv != nil && b.currentenv.state != nil {
		b.currentenv.state.StopPrefetcher()
	}

	b.currentenv = env
	return nil

}

/*
func (b *EcBatcher) buildBlock() {
	b.ecCommitCh <- struct{}{}
}
*/

//build block is callled by other thread, mainly rpc call thread
//therefore, need to be very caucios not to make race condition
//build block: get new pending txs from tx pool, and executes them, and then add to the already executed transactions.
//finally commit block, and return it the ec
func (b *EcBatcher) buildBlock(cBlkNo uint64) (*types.Block, error) {

	//firstly, check whethere there is correct snapshot block (block number comparison)
	pblock := b.pendingBlock()
	if pblock == nil {
		return nil, fmt.Errorf("block is not ready: new block %d, parent block %d", cBlkNo+1, cBlkNo)
	}
	// batcher current working(pending) block is behind the external consensus
	if pblock.NumberU64() <= (cBlkNo) {
		return nil, fmt.Errorf("geth works on old pending block: new block %d, parent block %d", pblock.NumberU64(), cBlkNo)
	}

	//if buidblock makes progress, then set the variable
	b.buildblockMu.Lock()
	b.isBuildBlockProcessing = true
	b.buildblockMu.Unlock()

	//execute new pending txs
	b.updateNewWork()
	//for testing whether tx event processing is meaningful
	//let's print some statistics for testing
	log.Debug("TX Summary", "newwork tx suggested", "newwork tx committed", b.currentenv.fTcount_s, b.currentenv.fTcount_c)
	log.Debug("TX Summary", "2nd tx pool:all", "2nd tx pool:new", "finally committed tx", b.currentenv.pTcount_s, b.currentenv.pTcount_n, b.currentenv.tcount)
	log.Debug("TX Error Summary", "low nonce", "high nonce", "gas limit", b.currentenv.errNonceLcnt, b.currentenv.errNonceHcnt, b.currentenv.errGasLcnt)
	//log.Trace("TX Error Summary", "tx not supported error", b.currentenv.errNotSupportedcnt)
	//log.Trace("TX Error Summary", "default error", b.currentenv.errDefaultcnt)

	//build block and update the chain
	err, blk := b.commit()

	if err != nil {
		b.buildblockMu.Lock()
		b.isBuildBlockProcessing = false
		b.buildblockMu.Unlock()

		return nil, fmt.Errorf("can not mine new block: new block %d, parent block %d", pblock.NumberU64(), cBlkNo)
	}

	b.buildblockMu.Lock()
	b.isBuildBlockProcessing = false
	b.buildblockMu.Unlock()
	//and then return
	return blk, nil
}

func (b *EcBatcher) commitTransaction(tx *types.Transaction, coinbase common.Address) ([]*types.Log, error) {
	snap := b.currentenv.state.Snapshot()

	chain := b.eth.BlockChain()

	receipt, err := core.ApplyTransaction(chain.Config(), chain, &coinbase, b.currentenv.gasPool, b.currentenv.state, b.currentenv.header, tx, &b.currentenv.header.GasUsed, vm.Config{})

	if err != nil {
		b.currentenv.state.RevertToSnapshot(snap)
		return nil, err
	}
	b.currentenv.txs = append(b.currentenv.txs, tx)
	b.currentenv.receipts = append(b.currentenv.receipts, receipt)

	return receipt.Logs, nil
}

//updateTransactions execute transactions on new pending trasanactions
func (b *EcBatcher) updateTransactions(txs *types.TransactionsByPriceAndNonce) {

	//when there are no txs, gasPool is not created
	//therefore, need to check and then make it
	if b.currentenv.gasPool == nil {
		b.currentenv.gasPool = new(core.GasPool).AddGas(b.currentenv.header.GasLimit)
	}

	//	var coalescedLogs []*types.Log

	for {
		// If we don't have enough gas for any further transactions then we're done
		if b.currentenv.gasPool.Gas() < params.TxGas {
			log.Trace("Not enough gas for further transactions", "have", b.currentenv.gasPool, "want", params.TxGas)
			break
		}
		// Retrieve the next transaction and abort if all done
		tx := txs.Peek()
		if tx == nil {
			break
		}
		b.currentenv.pTcount_s++

		//if there are too many transactions, let's break
		if b.currentenv.fTcount_s+b.currentenv.pTcount_s > maxReadyTxs {
			log.Debug("Too many pending transactions:breaking commitTransactions", "txnum", b.currentenv.fTcount_s+b.currentenv.pTcount_s)
			break
		}

		//let's check the tx whether it is already processed or not, by yim
		_, ok := b.currentenv.processedTxs[tx.Hash()]
		if !ok {
			b.currentenv.pTcount_n++
			// Error may be ignored here. The error has already been checked
			// during transaction acceptance is the transaction pool.
			//
			// We use the eip155 signer regardless of the current hf.
			from, _ := types.Sender(b.currentenv.signer, tx)
			// Check whether the tx is replay protected. If we're not in the EIP155 hf
			// phase, start ignoring the sender until we do.
			if tx.Protected() && !b.chainconfig.IsEIP155(b.currentenv.header.Number) {
				log.Trace("Ignoring reply protected transaction", "hash", tx.Hash(), "eip155", b.chainconfig.EIP155Block)

				txs.Pop()
				continue
			}
			// Start executing the transaction
			b.currentenv.state.Prepare(tx.Hash(), common.Hash{}, b.currentenv.tcount)

			_, err := b.commitTransaction(tx, b.coinbase)
			switch {
			case errors.Is(err, core.ErrGasLimitReached):
				b.currentenv.errGasLcnt++
				// Pop the current out-of-gas transaction without shifting in the next from the account
				log.Trace("Gas limit exceeded for current block", "sender", from)
				txs.Pop()

			case errors.Is(err, core.ErrNonceTooLow):
				b.currentenv.errNonceLcnt++
				// New head notification data race between the transaction pool and miner, shift
				log.Trace("Skipping transaction with low nonce", "sender", from, "nonce", tx.Nonce())
				txs.Shift()

			case errors.Is(err, core.ErrNonceTooHigh):
				b.currentenv.errNonceHcnt++
				// Reorg notification data race between the transaction pool and miner, skip account =
				log.Trace("Skipping account with hight nonce", "sender", from, "nonce", tx.Nonce())
				txs.Pop()

			case errors.Is(err, nil):
				// Everything ok, collect the logs and shift in the next transaction from the same account
				//				coalescedLogs = append(coalescedLogs, logs...)
				b.currentenv.tcount++
				txs.Shift()

			case errors.Is(err, core.ErrTxTypeNotSupported):
				b.currentenv.errNotSupportedcnt++
				// Pop the unsupported transaction without shifting in the next from the account
				log.Trace("Skipping unsupported transaction type", "sender", from, "type", tx.Type())
				txs.Pop()

			default:
				b.currentenv.errDefaultcnt++
				// Strange error, discard the transaction and get the next in line (note, the
				// nonce-too-high clause will prevent us from executing in vain).
				log.Debug("Transaction failed, account skipped", "hash", tx.Hash(), "err", err)
				txs.Shift()
			}
		} else {
			//let's move the pointer
			txs.Shift()
		}

	}

}

//committTransactions execute transactions. and return whether it is interrupted or not by newhead
func (b *EcBatcher) commitTransactions(txs *types.TransactionsByPriceAndNonce, coinbase common.Address, interrupt *int32) bool {

	// Short circuit if current is nil
	if b.currentenv == nil {
		return true
	}

	if b.currentenv.gasPool == nil {
		b.currentenv.gasPool = new(core.GasPool).AddGas(b.currentenv.header.GasLimit)
	}

	var coalescedLogs []*types.Log

	for {
		// In the following three cases, we will interrupt the execution of the transaction.
		// (1) new head block event arrival, the interrupt signal is 1
		// (2) worker start or restart, the interrupt signal is 1
		// (3) worker recreate the mining block with any newly arrived transactions, the interrupt signal is 2.
		// For the first two cases, the semi-finished work will be discarded.
		// For the third case, the semi-finished work will be submitted to the consensus engine.
		if interrupt != nil && atomic.LoadInt32(interrupt) != commitInterruptNone {
			//if newhead interrupt occurs, abort immediately
			return atomic.LoadInt32(interrupt) == commitInterruptNewHead
		}
		// If we don't have enough gas for any further transactions then we're done
		if b.currentenv.gasPool.Gas() < params.TxGas {
			log.Trace("Not enough gas for further transactions", "have", b.currentenv.gasPool, "want", params.TxGas)
			break
		}
		// Retrieve the next transaction and abort if all done
		tx := txs.Peek()
		if tx == nil {
			break
		}

		//by yim, for recoding processed tx
		//and then tx event received, let's filter using ths map
		b.currentenv.processedTxs[tx.Hash()] = tx
		b.currentenv.fTcount_s++

		//if there are too many transactions, let's break
		if b.currentenv.fTcount_s > maxReadyTxs {
			log.Debug("Too many pending transactions:breaking commitTransactions", "txnum", b.currentenv.fTcount_s)
			break
		}

		// Error may be ignored here. The error has already been checked
		// during transaction acceptance is the transaction pool.
		//
		// We use the eip155 signer regardless of the current hf.
		from, _ := types.Sender(b.currentenv.signer, tx)
		// Check whether the tx is replay protected. If we're not in the EIP155 hf
		// phase, start ignoring the sender until we do.
		if tx.Protected() && !b.chainconfig.IsEIP155(b.currentenv.header.Number) {
			log.Trace("Ignoring reply protected transaction", "hash", tx.Hash(), "eip155", b.chainconfig.EIP155Block)

			txs.Pop()
			continue
		}
		// Start executing the transaction
		b.currentenv.state.Prepare(tx.Hash(), common.Hash{}, b.currentenv.tcount)

		logs, err := b.commitTransaction(tx, coinbase)
		switch {
		case errors.Is(err, core.ErrGasLimitReached):
			b.currentenv.errGasLcnt++
			// Pop the current out-of-gas transaction without shifting in the next from the account
			log.Trace("Gas limit exceeded for current block", "sender", from)
			txs.Pop()

		case errors.Is(err, core.ErrNonceTooLow):
			b.currentenv.errNonceLcnt++
			// New head notification data race between the transaction pool and miner, shift
			log.Trace("Skipping transaction with low nonce", "sender", from, "nonce", tx.Nonce())
			txs.Shift()

		case errors.Is(err, core.ErrNonceTooHigh):
			b.currentenv.errNonceHcnt++
			// Reorg notification data race between the transaction pool and miner, skip account =
			log.Trace("Skipping account with hight nonce", "sender", from, "nonce", tx.Nonce())
			txs.Pop()

		case errors.Is(err, nil):
			// Everything ok, collect the logs and shift in the next transaction from the same account
			coalescedLogs = append(coalescedLogs, logs...)
			b.currentenv.tcount++
			txs.Shift()

		case errors.Is(err, core.ErrTxTypeNotSupported):
			b.currentenv.errNotSupportedcnt++
			// Pop the unsupported transaction without shifting in the next from the account
			log.Trace("Skipping unsupported transaction type", "sender", from, "type", tx.Type())
			txs.Pop()

		default:
			b.currentenv.errDefaultcnt++
			// Strange error, discard the transaction and get the next in line (note, the
			// nonce-too-high clause will prevent us from executing in vain).
			log.Debug("Transaction failed, account skipped", "hash", tx.Hash(), "err", err)
			txs.Shift()
		}
	}

	if !b.isRunning() && len(coalescedLogs) > 0 {
		// We don't push the pendingLogsEvent while we are mining. The reason is that
		// when we are mining, the worker will regenerate a mining block every 3 seconds.
		// In order to avoid pushing the repeated pendingLog, we disable the pending log pushing.

		// make a copy, the state caches the logs and these logs get "upgraded" from pending to mined
		// logs by filling in the block hash when the block was mined by the local miner. This can
		// cause a race condition if a log was "upgraded" before the PendingLogsEvent is processed.
		cpy := make([]*types.Log, len(coalescedLogs))
		for i, l := range coalescedLogs {
			cpy[i] = new(types.Log)
			*cpy[i] = *l
		}
		b.pendingLogsFeed.Send(cpy)
	}

	return false
}

// updateNewWork update a candidate block using the current pending tx
func (b *EcBatcher) updateNewWork() {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Fill the block with all available pending transactions.
	pending, err := b.eth.TxPool().Pending()

	if err != nil {
		log.Error("Failed to fetch pending transactions", "err", err)
		return
	}

	// Split the pending transactions into locals and remotes
	localTxs, remoteTxs := make(map[common.Address]types.Transactions), pending
	for _, account := range b.eth.TxPool().Locals() {

		if txs := remoteTxs[account]; len(txs) > 0 {
			delete(remoteTxs, account)
			localTxs[account] = txs
		}
	}

	if len(localTxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(b.currentenv.signer, localTxs)
		b.updateTransactions(txs)
	}
	if len(remoteTxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(b.currentenv.signer, remoteTxs)
		b.updateTransactions(txs)
	}

}

// commitNewWork generates the new block based on tx in the tx pool
func (b *EcBatcher) commitNewWork(interrupt *int32, noempty bool, timestamp int64) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	chain := b.eth.BlockChain()
	parent := chain.CurrentBlock()
	num := parent.Number()

	coinbase := b.coinbase
	//	timestamp := time.Now().Unix()

	//following 3 lines copied from ethash miner
	//if block generation is fast, following routine cause problem.
	// so i need to change it, yim
	/*
		if parent.Time() >= uint64(timestamp) {
			timestamp = int64(parent.Time() + 1)
		}
	*/
	if parent.Time() >= uint64(timestamp) {
		timestamp = int64(parent.Time())
	}

	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     num.Add(num, common.Big1),
		//GasLimit:   core.CalcGasLimit(parent, b.config.GasFloor, b.config.GasFloor),
		//not going to calculate, instead let's set the value from genesis file
		//GasLimit: b.config.GasCeil,
		GasLimit: GasCeil,
		Extra:    EcGethDummyExtra,
		Time:     uint64(timestamp),
	}

	header.Coinbase = coinbase

	if err := b.engine.Prepare(chain, header); err != nil {
		log.Error("Failed to prepare header for new block", "err", err)
		return
	}

	// Could potentially happen if starting to mine in an odd state.
	err := b.makeCurrent(parent, header)
	if err != nil {
		log.Error("Failed to create mining context", "err", err)
		return
	}

	//do not care uncles, deleting uncle things by yim

	// Fill the block with all available pending transactions.
	pending, err := b.eth.TxPool().Pending()

	if err != nil {
		log.Error("Failed to fetch pending transactions", "err", err)
		return
	}

	// empty block is necessary to keep the liveness of the network.
	if len(pending) == 0 {
		//don't need to call updateSnapshot
		//it will be called in mainloop
		//b.updateSnapshot()
		return
	}
	// Split the pending transactions into locals and remotes
	localTxs, remoteTxs := make(map[common.Address]types.Transactions), pending
	for _, account := range b.eth.TxPool().Locals() {
		if txs := remoteTxs[account]; len(txs) > 0 {
			delete(remoteTxs, account)
			localTxs[account] = txs
		}
	}

	if len(localTxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(b.currentenv.signer, localTxs)
		if b.commitTransactions(txs, coinbase, interrupt) {
			return
		}
	}
	if len(remoteTxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(b.currentenv.signer, remoteTxs)
		if b.commitTransactions(txs, coinbase, interrupt) {
			return
		}
	}

}

// commit runs any post-transaction state modifications, assembles the final block
// and commits new work if consensus engine is running.
// commit returns assembled block
func (b *EcBatcher) commit() (error, *types.Block) {

	chain := b.eth.BlockChain()

	// Deep copy receipts here to avoid interaction between different tasks.
	receipts := copyReceipts(b.currentenv.receipts)
	s := b.currentenv.state.Copy()

	block, err := b.engine.FinalizeAndAssemble(chain, b.currentenv.header, s, b.currentenv.txs, nil, receipts)

	if err != nil {
		log.Debug("FinalizeAndAssmbe", " can not build block", err.Error())
		return err, nil
	}
	log.Debug("FinalizeAndAssemble successful", "num fo txs", len(block.Transactions()))

	//creating task
	b.pendingTask = &task{receipts: receipts, state: s, block: block, createdAt: time.Now()}
	log.Info("Commit new mining work", "number", block.Number(), "sealhash", b.engine.SealHash(block.Header()),
		"uncles", 0, "txs", b.currentenv.tcount,
		"gas", block.GasUsed(), "fees", totalFees(block, receipts), "elapsed", common.PrettyDuration(time.Since(b.currentenv.tstart)))

	b.updateSnapshot()

	return nil, block
}

// copyReceipts makes a deep copy of the given receipts.
func copyReceipts(receipts []*types.Receipt) []*types.Receipt {
	result := make([]*types.Receipt, len(receipts))
	for i, l := range receipts {
		cpy := *l
		result[i] = &cpy
	}
	return result
}

// totalFees computes total consumed fees in ETH. Block transactions and receipts have to have the same order.
func totalFees(block *types.Block, receipts []*types.Receipt) *big.Float {
	feesWei := new(big.Int)
	for i, tx := range block.Transactions() {
		feesWei.Add(feesWei, new(big.Int).Mul(new(big.Int).SetUint64(receipts[i].GasUsed), tx.GasPrice()))
	}
	return new(big.Float).Quo(new(big.Float).SetInt(feesWei), new(big.Float).SetInt(big.NewInt(params.Ether)))
}

// updateSnapshot updates pending snapshot block and state.
// Note this function assumes the current variable is thread safe.
func (b *EcBatcher) updateSnapshot() {
	b.snapshotMu.Lock()
	defer b.snapshotMu.Unlock()

	// deleted uncle things by yim
	b.snapshotBlock = types.NewBlock(
		b.currentenv.header,
		b.currentenv.txs,
		nil,
		b.currentenv.receipts,
		trie.NewStackTrie(nil),
	)
	b.snapshotState = b.currentenv.state.Copy()

	log.Debug("updateSnapshot called", "blkno", b.snapshotBlock.NumberU64(), "GasLimt", b.snapshotBlock.GasLimit())

}

// clearing the snapshot block and state
//it must be called upon new head event
func (b *EcBatcher) clearPending() {

	b.snapshotMu.RLock()
	defer b.snapshotMu.RUnlock()

	b.pendingTask = nil
	b.snapshotBlock = nil
	b.snapshotState = nil

}

// pending returns the pending state and corresponding block.
func (b *EcBatcher) pending() (*types.Block, *state.StateDB) {
	// return a snapshot to avoid contention on currentMu mutex
	b.snapshotMu.RLock()
	defer b.snapshotMu.RUnlock()
	if b.snapshotState == nil {
		return nil, nil
	}
	return b.snapshotBlock, b.snapshotState.Copy()
}

// pendingBlock returns pending block.
func (b *EcBatcher) pendingBlock() *types.Block {
	// return a snapshot to avoid contention on currentMu mutex
	b.snapshotMu.RLock()
	defer b.snapshotMu.RUnlock()
	return b.snapshotBlock
}

// pendingBlock returns pending block.
func (b *EcBatcher) insertBlock(block *types.Block) error {

	// Commit block and state to database.

	//I don't think we need lock here
	if b.pendingTask == nil {
		log.Error("there is no pending task")
		return fmt.Errorf("no pending task")
	}

	var (
		sealhash  = b.engine.SealHash(block.Header())
		hash      = block.Hash()
		receipts  = make([]*types.Receipt, len(b.pendingTask.receipts))
		logs      []*types.Log
		createdAt = b.pendingTask.createdAt
	)

	for i, receipt := range b.pendingTask.receipts {
		// add block location fields
		receipt.BlockHash = hash
		receipt.BlockNumber = block.Number()
		receipt.TransactionIndex = uint(i)

		receipts[i] = new(types.Receipt)
		*receipts[i] = *receipt
		// Update the block hash in all logs since it is now available and not when the
		// receipt/log of individual transactions were created.
		for _, log := range receipt.Logs {
			log.BlockHash = hash
		}
		logs = append(logs, receipt.Logs...)
	}
	// Commit block and state to database.
	_, err := b.eth.BlockChain().WriteBlockWithState(block, receipts, logs, b.pendingTask.state, true)
	if err != nil {
		log.Error("Failed writing block to chain", "err", err)
		return fmt.Errorf("Failed writing block to chain", "err", err)
	}

	//after writing the block, new head event can happen immediately
	//that cause the clearPending, therefore, you must not use pendingTask anymore after writing block

	log.Info("Successfully sealed new block", "number", block.Number(), "sealhash", sealhash, "hash", hash,
		"elapsed", common.PrettyDuration(time.Since(createdAt)))

	log.Debug("more info", "GasLimt", block.GasLimit(), "GasUsed", block.GasUsed())

	// Broadcast the block and announce chain insertion event
	// I am going to use BroadcastBlock directly in the api.go
	//however, later consider usisng below
	// if you send this event, eth handler will broadcast block

	//b.mux.Post(core.NewMinedBlockEvent{Block: block})

	return nil
}
