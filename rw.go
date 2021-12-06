package dbw

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
)

const (
	noRowsAffected = 0

	// DefaultLimit is the default for search results when no limit is specified
	// via the WithLimit(...) option
	DefaultLimit = 10000
)

// RW uses a DB as a connection for it's read/write operations.  This is
// basically the primary type for the package's operations.
type RW struct {
	underlying *DB
}

// ensure that RW implements the interfaces of: Reader and Writer
var (
	_ Reader = (*RW)(nil)
	_ Writer = (*RW)(nil)
)

// New creates a new RW using an open DB. Note: there can by many RWs that share
// the same DB, since the DB manages the connection pool.
func New(underlying *DB) *RW {
	return &RW{underlying: underlying}
}

// DB returns the underlying DB
func (rw *RW) DB() *DB {
	return rw.underlying
}

// Exec will execute the sql with the values as parameters. The int returned
// is the number of rows affected by the sql. No options are currently
// supported.
func (rw *RW) Exec(_ context.Context, sql string, values []interface{}, _ ...Option) (int, error) {
	const op = "dbw.Exec"
	if rw.underlying == nil {
		return 0, fmt.Errorf("%s: missing underlying db: %w", op, ErrInternal)
	}
	if sql == "" {
		return noRowsAffected, fmt.Errorf("%s: missing sql: %w", op, ErrInvalidParameter)
	}
	db := rw.underlying.wrapped.Exec(sql, values...)
	if db.Error != nil {
		return noRowsAffected, fmt.Errorf("%s: %w", op, db.Error)
	}
	return int(db.RowsAffected), nil
}

func (rw *RW) primaryFieldsAreZero(_ context.Context, i interface{}) ([]string, bool, error) {
	const op = "dbw.primaryFieldsAreZero"
	var fieldNames []string
	tx := rw.underlying.wrapped.Model(i)
	if err := tx.Statement.Parse(i); err != nil {
		return nil, false, fmt.Errorf("%s: %w", op, ErrInvalidParameter)
	}
	for _, f := range tx.Statement.Schema.PrimaryFields {
		if f.PrimaryKey {
			if _, isZero := f.ValueOf(reflect.ValueOf(i)); isZero {
				fieldNames = append(fieldNames, f.Name)
			}
		}
	}
	return fieldNames, len(fieldNames) > 0, nil
}

func isNil(i interface{}) bool {
	if i == nil {
		return true
	}
	switch reflect.TypeOf(i).Kind() {
	case reflect.Ptr, reflect.Map, reflect.Array, reflect.Chan, reflect.Slice:
		return reflect.ValueOf(i).IsNil()
	}
	return false
}

func contains(ss []string, t string) bool {
	for _, s := range ss {
		if strings.EqualFold(s, t) {
			return true
		}
	}
	return false
}

func raiseErrorOnHooks(i interface{}) error {
	const op = "dbw.raiseErrorOnHooks"
	v := i
	valOf := reflect.ValueOf(i)
	if valOf.Kind() == reflect.Slice {
		if valOf.Len() == 0 {
			return nil
		}
		v = valOf.Index(0).Interface()
	}

	switch v.(type) {
	case
		// create hooks
		callbacks.BeforeCreateInterface,
		callbacks.AfterCreateInterface,
		callbacks.BeforeSaveInterface,
		callbacks.AfterSaveInterface,

		// update hooks
		callbacks.BeforeUpdateInterface,
		callbacks.AfterUpdateInterface,

		// delete hooks
		callbacks.BeforeDeleteInterface,
		callbacks.AfterDeleteInterface,

		// find hooks
		callbacks.AfterFindInterface:

		return fmt.Errorf("%s: gorm callback/hooks are not supported: %w", op, ErrInvalidParameter)
	}
	return nil
}

func (rw *RW) whereClausesFromOpts(_ context.Context, i interface{}, opts Options) (string, []interface{}, error) {
	const op = "dbw.whereClausesFromOpts"
	var where []string
	var args []interface{}
	if opts.WithVersion != nil {
		if *opts.WithVersion == 0 {
			return "", nil, fmt.Errorf("%s: with version option is zero: %w", op, ErrInvalidParameter)
		}
		mDb := rw.underlying.wrapped.Model(i)
		err := mDb.Statement.Parse(i)
		if err != nil && mDb.Statement.Schema == nil {
			return "", nil, fmt.Errorf("%s: (internal error) unable to parse stmt: %w", op, ErrUnknown)
		}
		if !contains(mDb.Statement.Schema.DBNames, "version") {
			return "", nil, fmt.Errorf("%s: %s does not have a version field: %w", op, mDb.Statement.Schema.Table, ErrInvalidParameter)
		}
		where = append(where, fmt.Sprintf("%s.version = ?", mDb.Statement.Schema.Table)) // we need to include the table name because of "on conflict" use cases
		args = append(args, opts.WithVersion)
	}
	if opts.withWhereClause != "" {
		where, args = append(where, opts.withWhereClause), append(args, opts.withWhereClauseArgs...)
	}
	return strings.Join(where, " and "), args, nil
}

func (rw *RW) primaryKeysWhere(_ context.Context, i interface{}) (string, []interface{}, error) {
	const op = "dbw.primaryKeysWhere"
	var fieldNames []string
	var fieldValues []interface{}
	tx := rw.underlying.wrapped.Model(i)
	if err := tx.Statement.Parse(i); err != nil {
		return "", nil, fmt.Errorf("%s: %w", op, err)
	}
	switch resourceType := i.(type) {
	case ResourcePublicIder:
		if resourceType.GetPublicId() == "" {
			return "", nil, fmt.Errorf("%s: missing primary key: %w", op, ErrInvalidParameter)
		}
		fieldValues = []interface{}{resourceType.GetPublicId()}
		fieldNames = []string{"public_id"}
	case ResourcePrivateIder:
		if resourceType.GetPrivateId() == "" {
			return "", nil, fmt.Errorf("%s: missing primary key: %w", op, ErrInvalidParameter)
		}
		fieldValues = []interface{}{resourceType.GetPrivateId()}
		fieldNames = []string{"private_id"}
	default:
		v := reflect.ValueOf(i)
		for _, f := range tx.Statement.Schema.PrimaryFields {
			if f.PrimaryKey {
				val, isZero := f.ValueOf(v)
				if isZero {
					return "", nil, fmt.Errorf("%s: primary field %s is zero: %w", op, f.Name, ErrInvalidParameter)
				}
				fieldNames = append(fieldNames, f.DBName)
				fieldValues = append(fieldValues, val)
			}
		}
	}
	if len(fieldNames) == 0 {
		return "", nil, fmt.Errorf("%s: no primary key(s) for %t: %w", op, i, ErrInvalidParameter)
	}
	clauses := make([]string, 0, len(fieldNames))
	for _, col := range fieldNames {
		clauses = append(clauses, fmt.Sprintf("%s = ?", col))
	}
	return strings.Join(clauses, " and "), fieldValues, nil
}

// LookupWhere will lookup the first resource using a where clause with parameters (it only returns the first one)
func (rw *RW) LookupWhere(_ context.Context, resource interface{}, where string, args ...interface{}) error {
	const op = "dbw.LookupWhere"
	if rw.underlying == nil {
		return fmt.Errorf("%s: missing underlying db: %w", op, ErrInvalidParameter)
	}
	if reflect.ValueOf(resource).Kind() != reflect.Ptr {
		return fmt.Errorf("%s: interface parameter must to be a pointer: %w", op, ErrInvalidParameter)
	}
	if err := raiseErrorOnHooks(resource); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if err := rw.underlying.wrapped.Where(where, args...).First(resource).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("%s: %w", op, ErrRecordNotFound)
		}
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// SearchWhere will search for all the resources it can find using a where
// clause with parameters. An error will be returned if args are provided without a
// where clause.
//
// Supports the WithLimit option.  If WithLimit < 0, then unlimited results are returned.
// If WithLimit == 0, then default limits are used for results.
// Supports the WithOrder and WithDebug options.
func (rw *RW) SearchWhere(ctx context.Context, resources interface{}, where string, args []interface{}, opt ...Option) error {
	const op = "dbw.SearchWhere"
	opts := GetOpts(opt...)
	if rw.underlying == nil {
		return fmt.Errorf("%s: missing underlying db: %w", op, ErrInvalidParameter)
	}
	if where == "" && len(args) > 0 {
		return fmt.Errorf("%s: args provided with empty where: %w", op, ErrInvalidParameter)
	}
	if err := raiseErrorOnHooks(resources); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if reflect.ValueOf(resources).Kind() != reflect.Ptr {
		return fmt.Errorf("%s: interface parameter must to be a pointer: %w", op, ErrInvalidParameter)
	}
	var err error
	db := rw.underlying.wrapped.WithContext(ctx)
	if opts.withOrder != "" {
		db = db.Order(opts.withOrder)
	}
	if opts.withDebug {
		db = db.Debug()
	}
	// Perform limiting
	switch {
	case opts.WithLimit < 0: // any negative number signals unlimited results
	case opts.WithLimit == 0: // zero signals the default value and default limits
		db = db.Limit(DefaultLimit)
	default:
		db = db.Limit(opts.WithLimit)
	}

	if where != "" {
		db = db.Where(where, args...)
	}

	// Perform the query
	err = db.Find(resources).Error
	if err != nil {
		// searching with a slice parameter does not return a gorm.ErrRecordNotFound
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
