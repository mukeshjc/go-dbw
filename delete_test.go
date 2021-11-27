package dbw_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/go-dbw"
	"github.com/hashicorp/go-dbw/internal/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDb_Delete(t *testing.T) {
	db, _ := dbw.TestSetup(t)
	testRw := dbw.New(db)
	newUser := func() *dbtest.TestUser {
		u, err := dbtest.NewTestUser()
		require.NoError(t, err)
		err = testRw.Create(context.Background(), u)
		require.NoError(t, err)
		return u
	}
	notFoundUser := func() *dbtest.TestUser {
		u, err := dbtest.NewTestUser()
		require.NoError(t, err)
		u.PublicId = "1111111"
		return u
	}

	successBeforeFn := func(_ interface{}) error {
		return nil
	}
	successAfterFn := func(_ interface{}, _ int) error {
		return nil
	}
	errFailedFn := errors.New("fail")
	failedBeforeFn := func(_ interface{}) error {
		return errFailedFn
	}
	failedAfterFn := func(_ interface{}, _ int) error {
		return errFailedFn
	}

	// seed some test users, so we won't just happen to get a false positive
	// with only 1 entry in the db
	for i := 0; i < 1000; i++ {
		_ = newUser()
	}

	type args struct {
		i   *dbtest.TestUser
		opt []dbw.Option
	}
	tests := []struct {
		name      string
		rw        *dbw.RW
		args      args
		want      int
		wantOplog bool
		wantFound bool
		wantErr   bool
		wantErrIs error
	}{
		{
			name: "simple",
			rw:   testRw,
			args: args{
				i:   newUser(),
				opt: []dbw.Option{dbw.WithDebug(true)},
			},
			want:    1,
			wantErr: false,
		},
		{
			name: "simple-with-before-after-success",
			rw:   testRw,
			args: args{
				i: newUser(),
				opt: []dbw.Option{
					dbw.WithBeforeWrite(successBeforeFn),
					dbw.WithAfterWrite(successAfterFn),
				},
			},
			want:    1,
			wantErr: false,
		},
		{
			name:      "nil-resource",
			rw:        testRw,
			args:      args{},
			want:      0,
			wantErr:   true,
			wantErrIs: dbw.ErrInvalidParameter,
		},
		{
			name: "nil-resource",
			rw:   testRw,
			args: args{
				i: &dbtest.TestUser{},
			},
			want:      0,
			wantErr:   true,
			wantErrIs: dbw.ErrInvalidParameter,
		},
		{
			name: "failed-with-before",
			rw:   testRw,
			args: args{
				i: newUser(),
				opt: []dbw.Option{
					dbw.WithBeforeWrite(failedBeforeFn),
				},
			},
			wantErr:   true,
			wantErrIs: errFailedFn,
		},
		{
			name: "failed-with-after",
			rw:   testRw,
			args: args{
				i: newUser(),
				opt: []dbw.Option{
					dbw.WithAfterWrite(failedAfterFn),
				},
			},
			wantErr:   true,
			wantErrIs: errFailedFn,
		},
		{
			name: "with-where-no-delete",
			rw:   testRw,
			args: args{
				i:   newUser(),
				opt: []dbw.Option{dbw.WithWhere("1 = ?", 2)},
			},
			wantFound: true,
			want:      0,
			wantErr:   false,
		},
		{
			name: "with-where-and-delete",
			rw:   testRw,
			args: args{
				i:   newUser(),
				opt: []dbw.Option{dbw.WithWhere("1 = ?", 1)},
			},
			want:    1,
			wantErr: false,
		},
		{
			name: "nil-underlying",
			rw:   &dbw.RW{},
			args: args{
				i: newUser(),
			},
			want:      0,
			wantErr:   true,
			wantErrIs: dbw.ErrInvalidParameter,
		},
		{
			name: "not-found",
			rw:   testRw,
			args: args{
				i: notFoundUser(),
			},
			want:    0,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)
			got, err := tt.rw.Delete(context.Background(), tt.args.i, tt.args.opt...)
			if tt.wantErr {
				require.Error(err)
				assert.ErrorIsf(err, tt.wantErrIs, "received unexpected error: %v", err)
				return
			}
			require.NoError(err)
			assert.Equal(tt.want, got)

			foundUser := tt.args.i.Clone().(*dbtest.TestUser)
			foundUser.PublicId = tt.args.i.PublicId
			err = tt.rw.LookupByPublicId(context.Background(), foundUser)
			if tt.wantFound {
				assert.NoError(err)
				assert.Equal(tt.args.i.PublicId, foundUser.PublicId)
				return
			}
			assert.Error(err)
			assert.ErrorIsf(err, dbw.ErrRecordNotFound, "received unexpected error: %v", err)
		})
		//
		// TODO: jimlambrt Nov 2021: Currently gorm doesn't properly support
		// deleting resources with composite primary keys.  see:
		// https://github.com/go-gorm/gorm/issues/4879
		// This needs to be resolved for Boundary and before this can be merged.
		//
		// t.Run("multi-column", func(t *testing.T) {
		// 	assert, require := assert.New(t), require.New(t)
		// 	user := testUser(t, testRw, "", "", "")
		// 	car := testCar(t, testRw)
		// 	rental := testRental(t, testRw, user.PublicId, car.PublicId)
		// 	rowsDeleted, err := testRw.Delete(context.Background(), rental, dbw.WithDebug(true))
		// 	require.NoError(err)
		// 	assert.Equal(1, rowsDeleted)
		// })
	}
}

func TestDb_DeleteItems(t *testing.T) {
	db, _ := dbw.TestSetup(t)
	testRw := dbw.New(db)

	createFn := func() []interface{} {
		results := []interface{}{}
		for i := 0; i < 10; i++ {
			u := testUser(t, testRw, "", "", "")
			results = append(results, u)
		}
		return results
	}
	createMixedFn := func() []interface{} {
		u, err := dbtest.NewTestUser()
		require.NoError(t, err)
		c, err := dbtest.NewTestCar()
		require.NoError(t, err)
		return []interface{}{
			u,
			c,
		}
	}

	successBeforeFn := func(_ interface{}) error {
		return nil
	}
	successAfterFn := func(_ interface{}, _ int) error {
		return nil
	}
	errFailedFn := errors.New("fail")
	failedBeforeFn := func(_ interface{}) error {
		return errFailedFn
	}
	failedAfterFn := func(_ interface{}, _ int) error {
		return errFailedFn
	}

	type args struct {
		deleteItems []interface{}
		opt         []dbw.Option
	}
	tests := []struct {
		name            string
		rw              *dbw.RW
		args            args
		wantRowsDeleted int
		wantOplogId     string
		wantOplogMsgs   bool
		wantErr         bool
		wantErrIs       error
	}{
		{
			name: "simple",
			rw:   dbw.New(db),
			args: args{
				deleteItems: createFn(),
			},
			wantRowsDeleted: 10,
			wantErr:         false,
		},
		{
			name: "simple-with-before-after-success",
			rw:   dbw.New(db),
			args: args{
				deleteItems: createFn(),
				opt: []dbw.Option{
					dbw.WithBeforeWrite(successBeforeFn),
					dbw.WithAfterWrite(successAfterFn),
				},
			},
			wantRowsDeleted: 10,
			wantErr:         false,
		},
		{
			name: "failed-with-before",
			rw:   testRw,
			args: args{
				deleteItems: createFn(),
				opt: []dbw.Option{
					dbw.WithBeforeWrite(failedBeforeFn),
				},
			},
			wantErr:   true,
			wantErrIs: errFailedFn,
		},
		{
			name: "failed-with-after",
			rw:   testRw,
			args: args{
				deleteItems: createFn(),
				opt: []dbw.Option{
					dbw.WithAfterWrite(failedAfterFn),
				},
			},
			wantErr:   true,
			wantErrIs: errFailedFn,
		},
		{
			name: "mixed items",
			rw:   testRw,
			args: args{
				deleteItems: createMixedFn(),
			},
			wantErr:   true,
			wantErrIs: dbw.ErrInvalidParameter,
		},
		{
			name: "bad opt: WithLookup",
			rw:   dbw.New(db),
			args: args{
				deleteItems: createFn(),
				opt:         []dbw.Option{dbw.WithLookup(true)},
			},
			wantErr:   true,
			wantErrIs: dbw.ErrInvalidParameter,
		},
		{
			name: "nil underlying",
			rw:   &dbw.RW{},
			args: args{
				deleteItems: createFn(),
			},
			wantErr:   true,
			wantErrIs: dbw.ErrInvalidParameter,
		},
		{
			name: "empty items",
			rw:   dbw.New(db),
			args: args{
				deleteItems: []interface{}{},
			},
			wantErr:   true,
			wantErrIs: dbw.ErrInvalidParameter,
		},
		{
			name: "nil items",
			rw:   dbw.New(db),
			args: args{
				deleteItems: nil,
			},
			wantErr:   true,
			wantErrIs: dbw.ErrInvalidParameter,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert, require := assert.New(t), require.New(t)
			rowsDeleted, err := tt.rw.DeleteItems(context.Background(), tt.args.deleteItems, tt.args.opt...)
			if tt.wantErr {
				require.Error(err)
				assert.ErrorIs(err, tt.wantErrIs)
				return
			}
			require.NoError(err)
			assert.Equal(tt.wantRowsDeleted, rowsDeleted)
			for _, item := range tt.args.deleteItems {
				u := dbtest.AllocTestUser()
				u.PublicId = item.(*dbtest.TestUser).PublicId
				err := tt.rw.LookupByPublicId(context.Background(), &u)
				require.Error(err)
				assert.ErrorIs(err, dbw.ErrRecordNotFound)
			}
		})
	}
}