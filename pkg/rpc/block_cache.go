package rpc

import (
	"sync"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"
)

type BlockCache struct {
	blocks   []dto.Block
	capacity int
	rw       sync.Mutex
}

func (cache *BlockCache) GetByHash(hash string) (*dto.Block, bool) {
	for i := 0; i < len(cache.blocks); i++ {
		if cache.blocks[i].Hash == hash {
			return &cache.blocks[i], true
		}
	}
	return nil, false
}

func (cache *BlockCache) Get(number uint64) (*dto.Block, bool) {
	for i := 0; i < len(cache.blocks); i++ {
		if cache.blocks[i].Number == number {
			return &cache.blocks[i], true
		}
	}
	return nil, false
}

func (cache *BlockCache) Add(block dto.Block) {
	if len(cache.blocks) == 0 {
		cache.blocks = append(cache.blocks, block)
		return
	}
	if len(cache.blocks)+1 > cache.capacity*2 {
		cache.blocks = cache.blocks[:cache.capacity]
	}
	cache.rw.Lock()
	defer cache.rw.Unlock()
	for i := 0; i < len(cache.blocks); i++ {
		if block.Number > cache.blocks[i].Number {
			cache.blocks = append(cache.blocks[:i+1], cache.blocks[i:]...)
			cache.blocks[i] = block
			return
		}
		if block.Number == cache.blocks[i].Number {
			return
		}
	}
}
