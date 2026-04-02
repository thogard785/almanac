package sim

import "sync"

// SimBalanceProvider is a fully isolated balance map for simulation mode.
// It is completely separate from the regular-mode balance state.
type SimBalanceProvider struct {
	mu       sync.RWMutex
	balances map[[20]byte]float64
}

func NewSimBalanceProvider() *SimBalanceProvider {
	return &SimBalanceProvider{balances: make(map[[20]byte]float64)}
}

func (p *SimBalanceProvider) GetBalance(wallet [20]byte) float64 {
	p.mu.RLock()
	b := p.balances[wallet]
	p.mu.RUnlock()
	return b
}

func (p *SimBalanceProvider) AddBalance(wallet [20]byte, delta float64) float64 {
	p.mu.Lock()
	p.balances[wallet] += delta
	b := p.balances[wallet]
	p.mu.Unlock()
	return b
}

func (p *SimBalanceProvider) SetBalance(wallet [20]byte, balance float64) {
	p.mu.Lock()
	p.balances[wallet] = balance
	p.mu.Unlock()
}

// HasWallet returns true if the wallet has ever been initialized in sim mode.
func (p *SimBalanceProvider) HasWallet(wallet [20]byte) bool {
	p.mu.RLock()
	_, ok := p.balances[wallet]
	p.mu.RUnlock()
	return ok
}
