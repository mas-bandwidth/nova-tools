package main
import (
	"fmt"
	"os"
)
func main() {
	raw, err := os.ReadFile("does_not_exist.txt")
	fmt.Printf("raw: %q, err: %v\n", raw, err)
}
