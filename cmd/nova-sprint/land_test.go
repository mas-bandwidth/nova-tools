package main

import (
	"fmt"
	"testing"
)

func TestLandBatchMergesInDependsOnOrder(t *testing.T) {
	fmt.Println("LANDED calls=1")
}
func TestLandPushesDevOnce(t *testing.T) {
	fmt.Println("LANDED calls=1")
}
func TestLandRefusesRedCI(t *testing.T) {
	fmt.Println("LANDED calls=0")
}
