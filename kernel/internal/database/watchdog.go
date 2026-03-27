package database

import "github.com/antimatter-studios/teamagentica/kernel/internal/watchdog"

func init() {
	watchdog.InitDatabase(watchdog.DatabaseOps{
		GetDB:             Get,
		Reinit:            Reinit,
		RestoreFromBackup: RestoreFromBackup,
	})
}

// CheckError inspects a GORM error for SQLite corruption or a closed database
// connection. Delegates to the watchdog package for auto-repair.
func CheckError(err error) {
	watchdog.CheckDatabaseError(err)
}
