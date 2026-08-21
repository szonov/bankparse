package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/szonov/bankparse"
	"github.com/szonov/bankparse/payment"
)

type outputDocument struct {
	Document payment.Document `json:"document"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <bank-file>\n", os.Args[0])
		os.Exit(2)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := readBankFile(os.Args[1], encoder); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readBankFile(path string, encoder *json.Encoder) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open bank file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat bank file: %w", err)
	}
	parser, err := bankparse.Open(f, info.Size())
	if err != nil {
		return err
	}

	if err := parser.WalkDocuments(func(document payment.Document) error {
		return encoder.Encode(outputDocument{Document: document})
	}); err != nil {
		return fmt.Errorf("parse bank file: %w", err)
	}
	return nil
}
