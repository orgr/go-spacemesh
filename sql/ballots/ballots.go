package ballots

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/spacemeshos/go-spacemesh/codec"
	"github.com/spacemeshos/go-spacemesh/common/types"
	"github.com/spacemeshos/go-spacemesh/sql"
)

// ErrConflict is raised if ballot is not valid.
var ErrConflict = errors.New("ballots: conflict")

func decodeBallot(id types.BallotID, sig, pubkey, body *bytes.Reader) (*types.Ballot, error) {
	sigBytes := make([]byte, sig.Len())
	if _, err := sig.Read(sigBytes); err != nil {
		if err != io.EOF {
			return nil, fmt.Errorf("copy sig: %w", err)
		}
		sigBytes = nil
	}
	pubkeyBytes := make([]byte, pubkey.Len())
	if _, err := pubkey.Read(pubkeyBytes); err != nil {
		if err != io.EOF {
			return nil, fmt.Errorf("copy pubkey: %w", err)
		}
		pubkeyBytes = nil
	}
	inner := types.InnerBallot{}
	if _, err := codec.DecodeFrom(body, &inner); err != nil {
		if err != io.EOF {
			return nil, fmt.Errorf("decode body of the %s: %w", id, err)
		}
	}
	ballot := types.NewExistingBallot(id, sigBytes, pubkeyBytes, inner)
	return &ballot, nil
}

// Add ballot to the database.
func Add(db sql.Executor, ballot *types.Ballot) error {
	bytes, err := codec.Encode(ballot.InnerBallot)
	if err != nil {
		return fmt.Errorf("encode ballot %s: %w", ballot.ID(), err)
	}
	var stored types.BallotID
	if _, err := db.Exec(`insert into ballots 
		(id, layer, signature, pubkey, ballot) 
		values (?1, ?2, ?3, ?4, ?5)
		on conflict(pubkey,layer) do update set invalid = 1 returning id;`,
		func(stmt *sql.Statement) {
			stmt.BindBytes(1, ballot.ID().Bytes())
			stmt.BindInt64(2, int64(ballot.LayerIndex.Value))
			stmt.BindBytes(3, ballot.Signature)
			stmt.BindBytes(4, ballot.SmesherID().Bytes())
			stmt.BindBytes(5, bytes)
		}, func(stmt *sql.Statement) bool {
			stmt.ColumnBytes(0, stored[:])
			return true
		}); err != nil {
		return fmt.Errorf("insert ballot %s: %w", ballot.ID(), err)
	}
	if ballot.ID() != stored {
		return fmt.Errorf("%w: %s with %s", ErrConflict, ballot.ID(), stored)
	}
	return nil
}

// Has a ballot in the database.
func Has(db sql.Executor, id types.BallotID) (bool, error) {
	rows, err := db.Exec("select 1 from ballots where id = ?1;",
		func(stmt *sql.Statement) {
			stmt.BindBytes(1, id.Bytes())
		}, nil,
	)
	if err != nil {
		return false, fmt.Errorf("has ballot %s: %w", id, err)
	}
	return rows > 0, nil
}

// Get ballot with id from database.
func Get(db sql.Executor, id types.BallotID) (rst *types.Ballot, err error) {
	if rows, err := db.Exec("select signature, pubkey, ballot from ballots where id = ?1;", func(stmt *sql.Statement) {
		stmt.BindBytes(1, id.Bytes())
	}, func(stmt *sql.Statement) bool {
		rst, err = decodeBallot(id, stmt.ColumnReader(0), stmt.ColumnReader(1), stmt.ColumnReader(2))
		return true
	}); err != nil {
		return nil, fmt.Errorf("get %s: %w", id, err)
	} else if rows == 0 {
		return nil, fmt.Errorf("%w ballot %s", sql.ErrNotFound, id)
	}
	return rst, nil
}

// Layer returns full body ballot for layer.
func Layer(db sql.Executor, lid types.LayerID) (rst []*types.Ballot, err error) {
	if _, err = db.Exec("select id, signature, pubkey, ballot from ballots where layer = ?1;", func(stmt *sql.Statement) {
		stmt.BindInt64(1, int64(lid.Value))
	}, func(stmt *sql.Statement) bool {
		id := types.BallotID{}
		stmt.ColumnBytes(0, id[:])
		var ballot *types.Ballot
		ballot, err = decodeBallot(id, stmt.ColumnReader(1), stmt.ColumnReader(2), stmt.ColumnReader(3))
		if err != nil {
			return false
		}
		rst = append(rst, ballot)
		return true
	}); err != nil {
		return nil, fmt.Errorf("ballots for layer %s: %w", lid, err)
	}
	return rst, err
}

// IDsInLayer returns ballots ids in the layer.
func IDsInLayer(db sql.Executor, lid types.LayerID) (rst []types.BallotID, err error) {
	if _, err := db.Exec("select id from ballots where layer = ?1;", func(stmt *sql.Statement) {
		stmt.BindInt64(1, int64(lid.Uint32()))
	}, func(stmt *sql.Statement) bool {
		id := types.BallotID{}
		stmt.ColumnBytes(0, id[:])
		rst = append(rst, id)
		return true
	}); err != nil {
		return nil, fmt.Errorf("ballots for layer %s: %w", lid, err)
	}
	return rst, err
}
