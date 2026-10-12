package main

import (
	"fmt"

	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

func main() {
	m := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	seed, _ := keys.Seed(m, "")
	for _, fam := range []string{keys.BTC, keys.LTC} {
		a, err := keys.DeriveAccount(seed, fam, keys.SimNet)
		if err != nil {
			panic(err)
		}
		addr, err := a.AddressAt(0)
		fmt.Println(fam, addr)
	}
}
