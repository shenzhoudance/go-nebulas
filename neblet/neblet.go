package neblet

import (
	"errors"
	"sync"

	"github.com/nebulasio/go-nebulas/account"
	"github.com/nebulasio/go-nebulas/consensus"
	"github.com/nebulasio/go-nebulas/consensus/dpos"
	"github.com/nebulasio/go-nebulas/core"
	"github.com/nebulasio/go-nebulas/metrics"
	"github.com/nebulasio/go-nebulas/neblet/pb"
	"github.com/nebulasio/go-nebulas/net/p2p"
	"github.com/nebulasio/go-nebulas/rpc"
	"github.com/nebulasio/go-nebulas/storage"
	nsync "github.com/nebulasio/go-nebulas/sync"
	"github.com/nebulasio/go-nebulas/util"
	"github.com/nebulasio/go-nebulas/util/byteutils"
	log "github.com/sirupsen/logrus"
)

var (
	// ErrNebletAlreadyRunning throws when the neblet is already running.
	ErrNebletAlreadyRunning = errors.New("neblet is already running")
)

var (
	storageSchemeVersionKey = []byte("scheme")
	storageSchemeVersionVal = []byte("0.1.0")
)

// Neblet manages ldife cycle of blockchain services.
type Neblet struct {
	config nebletpb.Config

	accountManager *account.Manager

	netService *p2p.NetService

	consensus consensus.Consensus

	blockChain *core.BlockChain

	snycManager *nsync.Manager

	apiServer rpc.Server

	managementServer rpc.Server

	lock sync.RWMutex

	eventEmitter *core.EventEmitter

	running bool
}

// New returns a new neblet.
func New(config nebletpb.Config) *Neblet {
	n := &Neblet{config: config}
	n.accountManager = account.NewManager(n)
	return n
}

// Setup setup neblet
func (n *Neblet) Setup() error {
	var err error
	//var err error
	n.netService, err = p2p.NewNetService(n)
	if err != nil {
		log.Error("new NetService occurs error ", err)
		return err
	}
	storage, err := storage.NewDiskStorage(n.config.Chain.Datadir)
	// storage, err := storage.NewMemoryStorage()
	if err != nil {
		return err
	}
	if err := n.CheckSchemeVersion(storage); err != nil {
		return err
	}
	n.blockChain, err = core.NewBlockChain(core.TestNetID, storage)
	if err != nil {
		return err
	}

	n.eventEmitter = core.NewEventEmitter()

	n.blockChain.BlockPool().RegisterInNetwork(n.netService)
	gasPrice := util.NewUint128FromString(n.config.Chain.GasPrice)
	gasLimit := util.NewUint128FromString(n.config.Chain.GasLimit)
	n.blockChain.TransactionPool().SetGasConfig(gasPrice, gasLimit)
	n.blockChain.TransactionPool().RegisterInNetwork(n.netService)

	n.consensus, err = dpos.NewDpos(n)
	if err != nil {
		return err
	}
	n.blockChain.SetConsensusHandler(n.consensus)

	// start sync service
	n.snycManager = nsync.NewManager(n.blockChain, n.consensus, n.netService)

	n.apiServer = rpc.NewAPIServer(n)
	return nil
}

// Start starts the services of the neblet.
func (n *Neblet) Start() error {
	var err error
	n.lock.Lock()
	defer n.lock.Unlock()
	log.Info("Starting neblet...")

	if n.running {
		return ErrNebletAlreadyRunning
	}
	n.running = true

	// start.
	if err = n.netService.Start(); err != nil {
		return err
	}
	n.blockChain.BlockPool().Start()
	n.blockChain.TransactionPool().Start()
	n.eventEmitter.Start()
	n.consensus.Start()
	n.snycManager.Start()
	go n.apiServer.Start()
	go n.apiServer.RunGateway()

	if n.config.Stats.EnableMetrics {
		go metrics.Start(n)
	}

	// TODO: error handling
	return nil
}

// Stop stops the services of the neblet.
func (n *Neblet) Stop() error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.Info("Stopping neblet...")

	if n.consensus != nil {
		n.consensus.Stop()
		n.consensus = nil
	}

	if n.blockChain != nil {
		n.blockChain.BlockPool().Stop()
		n.blockChain = nil
	}

	if n.eventEmitter != nil {
		n.eventEmitter.Stop()
		n.eventEmitter = nil
	}

	if n.netService != nil {
		n.netService.Stop()
		n.netService = nil
	}

	if n.apiServer != nil {
		n.apiServer.Stop()
		n.apiServer = nil
	}

	if n.managementServer != nil {
		n.managementServer.Stop()
		n.managementServer = nil
	}

	if n.config.Stats.EnableMetrics {
		metrics.Stop()
	}

	n.accountManager = nil

	n.running = false

	return nil
}

// Config returns neblet configuration.
func (n *Neblet) Config() nebletpb.Config {
	return n.config
}

// BlockChain returns block chain reference.
func (n *Neblet) BlockChain() *core.BlockChain {
	return n.blockChain
}

// EventEmitter returns eventEmitter reference.
func (n *Neblet) EventEmitter() *core.EventEmitter {
	return n.eventEmitter
}

// AccountManager returns account manager reference.
func (n *Neblet) AccountManager() *account.Manager {
	return n.accountManager
}

// NetService returns p2p manager reference.
func (n *Neblet) NetService() *p2p.NetService {
	return n.netService
}

// CheckSchemeVersion checks if the storage scheme version is compatiable
func (n *Neblet) CheckSchemeVersion(storage storage.Storage) error {
	version, err := storage.Get(storageSchemeVersionKey)
	if err != nil {
		storage.Put(storageSchemeVersionKey, storageSchemeVersionVal)
		return nil
	}
	if !byteutils.Equal(version, storageSchemeVersionVal) {
		return errors.New("incompatible storage schema version, pls migrate your storage")
	}
	return nil
}
