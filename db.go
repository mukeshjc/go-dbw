package dbw

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/hashicorp/go-hclog"
	"github.com/jackc/pgconn"

	_ "github.com/jackc/pgx/v4"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type DbType int

const (
	UnknownDB DbType = 0
	Postgres  DbType = 1
	Sqlite    DbType = 2
)

func (db DbType) String() string {
	return [...]string{
		"unknown",
		"postgres",
		"sqlite",
	}[db]
}

func StringToDbType(dialect string) (DbType, error) {
	switch dialect {
	case "postgres":
		return Postgres, nil
	case "sqlite":
		return Sqlite, nil
	default:
		return UnknownDB, fmt.Errorf("%s is an unknown dialect", dialect)
	}
}

// DB is a wrapper around the ORM
type DB struct {
	wrapped *gorm.DB
}

// DbType will return the DbType of the connection
func (db *DB) DbType() (DbType, error) {
	return StringToDbType(db.wrapped.Dialector.Name())
}

// Debug will enable/disable debug info for the connection
func (db *DB) Debug(on bool) {
	if on {
		// info level in the Gorm domain which maps to a debug level in the boundary domain
		db.wrapped.Logger = logger.Default.LogMode(logger.Info)
	} else {
		// the default level in the gorm domain is: error level
		db.wrapped.Logger = logger.Default.LogMode(logger.Error)
	}
}

// SqlDB returns the underlying sql.DB
//
// Note: This func is not named DB(), because our choice of ORM (gorm) which is
// embedded for various reasons, already exports a DB() func and it's base type
// is also an export DB type
func (d *DB) SqlDB(ctx context.Context) (*sql.DB, error) {
	const op = "dbw.(DB).SqlDB"
	if d.wrapped == nil {
		return nil, fmt.Errorf("%s: missing underlying database: %w", op, ErrInternal)
	}
	return d.wrapped.DB()
}

// Close the underlying sql.DB
func (d *DB) Close(ctx context.Context) error {
	const op = "dbw.(DB).Close"
	if d.wrapped == nil {
		return fmt.Errorf("%s: missing underlying database: %w", op, ErrInternal)
	}
	underlying, err := d.wrapped.DB()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return underlying.Close()
}

// Open a database connection which is long-lived. The options of
// WithGormFormatter and WithMaxOpenConnections are supported.
//
// Note: Consider if you need to call Close() on the returned DB.  Typically the
// answer is no, but there are occasions when it's necessary.  See the sql.DB
// docs for more information.
func Open(dbType DbType, connectionUrl string, opt ...Option) (*DB, error) {
	const op = "dbw.Open"
	if connectionUrl == "" {
		return nil, fmt.Errorf("%s: missing connection url: %w", op, ErrInvalidParameter)
	}
	var dialect gorm.Dialector
	switch dbType {
	case Postgres:
		dialect = postgres.New(postgres.Config{
			DSN: connectionUrl,
		},
		)
	case Sqlite:
		dialect = sqlite.Open(connectionUrl)

	default:
		return nil, fmt.Errorf("unable to open %s database type", dbType)
	}
	return openDialector(dialect, opt...)
}

// Dialector provides a set of functions the database dialect must satisfy.
// It's a simple wrapper of the gorm.Dialector and provides the ability to open
// any support gorm dialect driver.
type Dialector interface {
	gorm.Dialector
}

// OpenWith will open a database connection using a Dialector which is
// long-lived. The options of WithGormFormatter and WithMaxOpenConnections are
// supported.
//
// Note: Consider if you need to call Close() on the returned DB.  Typically the
// answer is no, but there are occasions when it's necessary.  See the sql.DB
// docs for more information.
func OpenWith(dialector Dialector, opt ...Option) (*DB, error) {
	return openDialector(dialector, opt...)
}

func openDialector(dialect gorm.Dialector, opt ...Option) (*DB, error) {
	db, err := gorm.Open(dialect, &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("unable to open database: %w", err)
	}
	opts := GetOpts(opt...)
	if opts.withGormFormatter != nil {
		newLogger := logger.New(
			getGormLogger(opts.withGormFormatter),
			logger.Config{
				LogLevel: logger.Error, // Log level
				Colorful: false,        // Disable color
			},
		)
		db = db.Session(&gorm.Session{Logger: newLogger})
	}
	if opts.withMaxOpenConnections > 0 {
		if opts.withMinOpenConnections > 0 && (opts.withMaxOpenConnections < opts.withMinOpenConnections) {
			return nil, fmt.Errorf("unable to create db object with dialect %s: %s", dialect, fmt.Sprintf("max_open_connections must be unlimited by setting 0 or at least %d", opts.withMinOpenConnections))
		}
		underlyingDB, err := db.DB()
		if err != nil {
			return nil, fmt.Errorf("unable retreive db: %w", err)
		}
		underlyingDB.SetMaxOpenConns(opts.withMaxOpenConnections)
	}
	return &DB{wrapped: db}, nil
}

type gormLogger struct {
	logger hclog.Logger
}

func (g gormLogger) Printf(msg string, values ...interface{}) {
	if len(values) > 1 {
		switch values[1].(type) {
		case *pgconn.PgError:
			g.logger.Trace("error from database adapter", "location", values[0], "error", values[1])
		}
	}
}

func getGormLogger(log hclog.Logger) gormLogger {
	return gormLogger{logger: log}
}
