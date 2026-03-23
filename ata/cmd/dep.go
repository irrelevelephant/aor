package cmd

import (
	"fmt"

	"aor/ata/db"
	"aor/ata/model"
)

func Dep(d *db.DB, args []string) error {
	if len(args) == 0 {
		return exitUsage(`usage: ata dep <subcommand> [args]

Subcommands:
  add TASK DEPENDS_ON        Add a dependency (TASK is blocked by DEPENDS_ON)
  rm  TASK DEPENDS_ON        Remove a dependency
  list TASK                  Show dependencies for a task
  propagate SOURCE NEW_TASK  Copy SOURCE's dependents to also depend on NEW_TASK`)
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "add":
		return depAdd(d, rest)
	case "rm", "remove":
		return depRemove(d, rest)
	case "list", "ls":
		return depList(d, rest)
	case "propagate":
		return depPropagate(d, rest)
	default:
		return fmt.Errorf("unknown dep subcommand: %s", sub)
	}
}

func depAdd(d *db.DB, args []string) error {
	if len(args) < 2 {
		return exitUsage("usage: ata dep add TASK DEPENDS_ON")
	}

	taskID := args[0]
	dependsOnID := args[1]

	if err := d.AddDep(taskID, dependsOnID); err != nil {
		return err
	}

	task, _ := d.GetTask(taskID)
	dep, _ := d.GetTask(dependsOnID)
	fmt.Printf("%s (%s) now depends on %s (%s)\n", taskID, task.Title, dependsOnID, dep.Title)
	return nil
}

func depRemove(d *db.DB, args []string) error {
	if len(args) < 2 {
		return exitUsage("usage: ata dep rm TASK DEPENDS_ON")
	}

	taskID := args[0]
	dependsOnID := args[1]

	if err := d.RemoveDep(taskID, dependsOnID); err != nil {
		return err
	}

	fmt.Printf("removed dependency: %s -> %s\n", taskID, dependsOnID)
	return nil
}

func depPropagate(d *db.DB, args []string) error {
	if len(args) < 2 {
		return exitUsage("usage: ata dep propagate SOURCE NEW_TASK")
	}

	sourceID := args[0]
	newID := args[1]

	added, err := d.PropagateDeps(sourceID, newID)
	if err != nil {
		return err
	}

	fmt.Printf("propagated %d dependency(ies) from %s to %s\n", added, sourceID, newID)
	return nil
}

func depList(d *db.DB, args []string) error {
	if len(args) == 0 {
		return exitUsage("usage: ata dep list TASK")
	}

	taskID := args[0]

	task, err := d.GetTask(taskID)
	if err != nil {
		return err
	}

	blockers, err := d.GetBlockers(taskID, false)
	if err != nil {
		return err
	}

	blocking, err := d.GetBlocking(taskID)
	if err != nil {
		return err
	}

	fmt.Printf("%s: %s\n", task.ID, task.Title)

	if len(blockers) > 0 {
		fmt.Println("\nBlocked by:")
		for _, b := range blockers {
			status := b.Status
			if b.Status == model.StatusClosed {
				status = "closed (resolved)"
			}
			fmt.Printf("  %-4s %-12s %s\n", b.ID, status, b.Title)
		}
	} else {
		fmt.Println("\nNo dependencies (not blocked)")
	}

	if len(blocking) > 0 {
		fmt.Println("\nBlocks:")
		for _, b := range blocking {
			fmt.Printf("  %-4s %-12s %s\n", b.ID, b.Status, b.Title)
		}
	}

	return nil
}
