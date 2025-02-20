package sim

import (
	"math/rand"

	"github.com/spacemeshos/go-spacemesh/common/types"
	"github.com/spacemeshos/go-spacemesh/log"
	"github.com/spacemeshos/go-spacemesh/signing"
)

// GenOpt for configuring Generator.
type GenOpt func(*Generator)

// WithSeed configures seed for Generator. By default 0 is used.
func WithSeed(seed int64) GenOpt {
	return func(g *Generator) {
		g.rng = rand.New(rand.NewSource(seed))
	}
}

// WithLayerSize configures average layer size.
func WithLayerSize(size uint32) GenOpt {
	return func(g *Generator) {
		g.conf.LayerSize = size
	}
}

// WithLogger configures logger.
func WithLogger(logger log.Log) GenOpt {
	return func(g *Generator) {
		g.logger = logger
	}
}

// WithPath configures path for persistent databases.
func WithPath(path string) GenOpt {
	return func(g *Generator) {
		g.conf.Path = path
	}
}

// WithStates creates n states.
func WithStates(n int) GenOpt {
	if n == 0 {
		panic("generator without attached state is not supported")
	}
	return func(g *Generator) {
		g.conf.StateInstances = n
	}
}

func withRng(rng *rand.Rand) GenOpt {
	return func(g *Generator) {
		g.rng = rng
	}
}

func withConf(conf config) GenOpt {
	return func(g *Generator) {
		g.conf = conf
	}
}

func withLayers(layers []*types.Layer) GenOpt {
	return func(g *Generator) {
		g.layers = layers
	}
}

type config struct {
	Path           string
	LayerSize      uint32
	LayersPerEpoch uint32
	StateInstances int
}

func defaults() config {
	return config{
		LayerSize:      30,
		LayersPerEpoch: types.GetLayersPerEpoch(),
		StateInstances: 1,
	}
}

// New creates Generator instance.
func New(opts ...GenOpt) *Generator {
	g := &Generator{
		rng:       rand.New(rand.NewSource(0)),
		conf:      defaults(),
		logger:    log.NewNop(),
		reordered: map[types.LayerID]types.LayerID{},
	}
	for _, opt := range opts {
		opt(g)
	}
	// TODO support multiple persist states.
	for i := 0; i < g.conf.StateInstances; i++ {
		g.states = append(g.states, newState(g.logger, g.conf))
	}
	return g
}

// Generator for layers of blocks.
type Generator struct {
	logger log.Log
	rng    *rand.Rand
	conf   config

	states []State

	nextLayer types.LayerID
	// key is when to return => value is the layer to return
	reordered   map[types.LayerID]types.LayerID
	layers      []*types.Layer
	units       [2]int
	activations []types.ATXID

	keys []*signing.EdSigner
}

// SetupOpt configures setup.
type SetupOpt func(g *setupConf)

// WithSetupMinerRange number of miners will be selected between low and high values.
func WithSetupMinerRange(low, high int) SetupOpt {
	return func(conf *setupConf) {
		conf.Miners = [2]int{low, high}
	}
}

// WithSetupUnitsRange adjusts units of the ATXs, which will directly affect block weight.
func WithSetupUnitsRange(low, high int) SetupOpt {
	return func(conf *setupConf) {
		conf.Units = [2]int{low, high}
	}
}

type setupConf struct {
	Miners [2]int
	Units  [2]int
}

func defaultSetupConf() setupConf {
	return setupConf{
		Miners: [2]int{30, 30},
		Units:  [2]int{10, 10},
	}
}

// GetState at index.
func (g *Generator) GetState(i int) State {
	return g.states[i]
}

func (g *Generator) addState(state State) {
	g.states = append(g.states, state)
}

func (g *Generator) popState(i int) State {
	state := g.states[i]
	copy(g.states[i:], g.states[i+1:])
	g.states[len(g.states)-1] = State{}
	g.states = g.states[:len(g.states)-1]
	return state
}

// Setup should be called before running Next.
func (g *Generator) Setup(opts ...SetupOpt) {
	conf := defaultSetupConf()
	for _, opt := range opts {
		opt(&conf)
	}
	g.units = conf.Units
	if len(g.layers) == 0 {
		g.layers = append(g.layers, types.GenesisLayer())
	}
	last := g.layers[len(g.layers)-1]
	g.nextLayer = last.Index().Add(1)

	miners := intInRange(g.rng, conf.Miners)
	g.activations = make([]types.ATXID, miners)

	for i := 0; i < miners; i++ {
		g.keys = append(g.keys, signing.NewEdSignerFromRand(g.rng))
	}
}

func (g *Generator) generateAtxs() {
	for i := range g.activations {
		units := intInRange(g.rng, g.units)
		address := types.Address{}
		_, _ = g.rng.Read(address[:])

		nipost := types.NIPostChallenge{
			NodeID:     types.NodeID{Key: address.Hex()},
			StartTick:  1,
			EndTick:    2,
			PubLayerID: g.nextLayer.Sub(1),
		}
		atx := types.NewActivationTx(nipost, address, nil, uint(units), nil)

		g.activations[i] = atx.ID()

		for _, state := range g.states {
			state.OnActivationTx(atx)
		}
	}
}
