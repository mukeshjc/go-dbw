package dbw

import (
	"context"
	"fmt"
	"reflect"

	"gorm.io/gorm"
)

// Update an object in the db, fieldMask is required and provides
// field_mask.proto paths for fields that should be updated. The i interface
// parameter is the type the caller wants to update in the db and its fields are
// set to the update values. setToNullPaths is optional and provides
// field_mask.proto paths for the fields that should be set to null.
// fieldMaskPaths and setToNullPaths must not intersect. The caller is
// responsible for the transaction life cycle of the writer and if an error is
// returned the caller must decide what to do with the transaction, which almost
// always should be to rollback.  Update returns the number of rows updated.
//
// Supported options: WithBeforeWrite, WithAfterWrite, WithWhere, WithDebug, and
// WithVersion. If WithVersion is used, then the update will include the version
// number in the update where clause, which basically makes the update use
// optimistic locking and the update will only succeed if the existing rows
// version matches the WithVersion option. Zero is not a valid value for the
// WithVersion option and will return an error. WithWhere allows specifying an
// additional constraint on the operation in addition to the PKs. WithDebug will
// turn on debugging for the update call.
func (rw *RW) Update(ctx context.Context, i interface{}, fieldMaskPaths []string, setToNullPaths []string, opt ...Option) (int, error) {
	const op = "dbw.Update"
	if rw.underlying == nil {
		return NoRowsAffected, fmt.Errorf("%s: missing underlying db: %w", op, ErrInvalidParameter)
	}
	if isNil(i) {
		return NoRowsAffected, fmt.Errorf("%s: missing interface: %w", op, ErrInvalidParameter)
	}
	if len(fieldMaskPaths) == 0 && len(setToNullPaths) == 0 {
		return NoRowsAffected, fmt.Errorf("%s: both fieldMaskPaths and setToNullPaths are missing: %w", op, ErrInvalidParameter)
	}
	opts := getOpts(opt...)

	// we need to filter out some non-updatable fields (like: CreateTime, etc)
	fieldMaskPaths = filterPaths(fieldMaskPaths)
	setToNullPaths = filterPaths(setToNullPaths)
	if len(fieldMaskPaths) == 0 && len(setToNullPaths) == 0 {
		return NoRowsAffected, fmt.Errorf("%s: after filtering non-updated fields, there are no fields left in fieldMaskPaths or setToNullPaths: %w", op, ErrInvalidParameter)
	}

	updateFields, err := UpdateFields(i, fieldMaskPaths, setToNullPaths)
	if err != nil {
		return NoRowsAffected, fmt.Errorf("%s: getting update fields failed: %w", op, err)
	}
	if len(updateFields) == 0 {
		return NoRowsAffected, fmt.Errorf("%s: no fields matched using fieldMaskPaths %s: %w", op, fieldMaskPaths, ErrInvalidParameter)
	}

	names, isZero, err := rw.primaryFieldsAreZero(ctx, i)
	if err != nil {
		return NoRowsAffected, fmt.Errorf("%s: %w", op, err)
	}
	if isZero {
		return NoRowsAffected, fmt.Errorf("%s: primary key is not set for: %s: %w", op, names, ErrInvalidParameter)
	}

	mDb := rw.underlying.wrapped.Model(i)
	err = mDb.Statement.Parse(i)
	if err != nil || mDb.Statement.Schema == nil {
		return NoRowsAffected, fmt.Errorf("%s: internal error: unable to parse stmt: %w", op, err)
	}
	reflectValue := reflect.Indirect(reflect.ValueOf(i))
	for _, pf := range mDb.Statement.Schema.PrimaryFields {
		if _, isZero := pf.ValueOf(reflectValue); isZero {
			return NoRowsAffected, fmt.Errorf("%s: primary key %s is not set: %w", op, pf.Name, ErrInvalidParameter)
		}
		if contains(fieldMaskPaths, pf.Name) {
			return NoRowsAffected, fmt.Errorf("%s: not allowed on primary key field %s: %w", op, pf.Name, ErrInvalidFieldMask)
		}
	}

	if !opts.withSkipVetForWrite {
		if vetter, ok := i.(VetForWriter); ok {
			if err := vetter.VetForWrite(ctx, rw, UpdateOp, WithFieldMaskPaths(fieldMaskPaths), WithNullPaths(setToNullPaths)); err != nil {
				return NoRowsAffected, fmt.Errorf("%s: %w", op, err)
			}
		}
	}
	if opts.withBeforeWrite != nil {
		if err := opts.withBeforeWrite(i); err != nil {
			return NoRowsAffected, fmt.Errorf("%s: error before write: %w", op, err)
		}
	}
	underlying := rw.underlying.wrapped.Model(i)
	if opts.withDebug {
		underlying = underlying.Debug()
	}
	switch {
	case opts.WithVersion != nil || opts.withWhereClause != "":
		where, args, err := rw.whereClausesFromOpts(ctx, i, opts)
		if err != nil {
			return NoRowsAffected, fmt.Errorf("%s: %w", op, err)
		}
		underlying = underlying.Where(where, args...).Updates(updateFields)
	default:
		underlying = underlying.Updates(updateFields)
	}
	if underlying.Error != nil {
		if underlying.Error == gorm.ErrRecordNotFound {
			return NoRowsAffected, fmt.Errorf("%s: %w", op, gorm.ErrRecordNotFound)
		}
		return NoRowsAffected, fmt.Errorf("%s: %w", op, err)
	}
	rowsUpdated := int(underlying.RowsAffected)
	if rowsUpdated > 0 && (opts.withAfterWrite != nil) {
		if err := opts.withAfterWrite(i, rowsUpdated); err != nil {
			return rowsUpdated, fmt.Errorf("%s: error after write: %w", op, err)
		}
	}
	// we need to force a lookupAfterWrite so the resource returned is correctly initialized
	// from the db
	opt = append(opt, WithLookup(true))
	if err := rw.lookupAfterWrite(ctx, i, opt...); err != nil {
		return NoRowsAffected, fmt.Errorf("%s: %w", op, err)
	}
	return rowsUpdated, nil
}
