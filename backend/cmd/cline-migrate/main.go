// cline-migrate defaults to read-only preview. Writes require exact approval and
// an explicit declaration that every gateway/worker is stopped and drained.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/clinemigration"
	_ "github.com/lib/pq"
)

func readJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 1<<20 {
		return errors.New("invalid or oversized input file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return errors.New("trailing input")
	}
	return nil
}
func run() error {
	action := flag.String("action", "preview", "preview, apply or rollback")
	input := flag.String("input", "", "specification for preview, or approved plan for writes")
	output := flag.String("output", "", "new plan file for preview; existing files are never overwritten")
	approval := flag.String("approve", "", "exact plan approval digest")
	maintenance := flag.String("maintenance-confirm", "", "ALL_WORKERS_STOPPED after disabling and draining the account")
	flag.Parse()
	if *input == "" || (*action != "preview" && *action != "apply" && *action != "rollback") {
		return errors.New("provide --input and a valid --action")
	}
	dsn := os.Getenv("CLINE_MIGRATION_DSN")
	if dsn == "" {
		return errors.New("set CLINE_MIGRATION_DSN without placing it in shell arguments")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return errors.New("database configuration unavailable")
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if *action == "preview" {
		if *output == "" {
			return errors.New("preview requires a new --output plan file")
		}
		var spec clinemigration.Spec
		if readJSON(*input, &spec) != nil {
			return errors.New("invalid migration specification")
		}
		plan, err := clinemigration.Preview(ctx, db, spec)
		if err != nil {
			return errors.New(clinemigration.SafeError(err))
		}
		file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("could not create the exclusive plan file")
		}
		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(plan)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return errors.New("could not write the plan file")
		}
		fmt.Println("Read-only preview:", plan.Summary())
		fmt.Println(plan.Warning)
		return nil
	}
	var plan clinemigration.Plan
	if readJSON(*input, &plan) != nil || clinemigration.ValidatePlan(plan) != nil {
		return errors.New("invalid approved plan")
	}
	if *action == "apply" {
		err = clinemigration.Apply(ctx, db, plan, *approval, *maintenance)
	} else {
		err = clinemigration.Rollback(ctx, db, plan, *approval, *maintenance)
	}
	if err != nil {
		return errors.New(clinemigration.SafeError(err))
	}
	fmt.Println("Completed", *action, plan.Summary())
	fmt.Println("Account remains unschedulable. No API-key permissions, prices or history were rewritten.")
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
