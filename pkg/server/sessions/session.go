/*
Copyright 2021 CodeNotary, Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sessions

import (
	"context"
	"github.com/codenotary/immudb/embedded/multierr"
	"github.com/codenotary/immudb/pkg/api/schema"
	"github.com/codenotary/immudb/pkg/auth"
	"github.com/codenotary/immudb/pkg/database"
	"github.com/codenotary/immudb/pkg/logger"
	"github.com/codenotary/immudb/pkg/server/sessions/internal/transactions"
	"github.com/rs/xid"
	"google.golang.org/grpc/metadata"
	"sync"
	"time"
)

type Status int64

const (
	Active Status = iota
	Idle
	Dead
)

type Session struct {
	mux                sync.RWMutex
	id                 string
	state              Status
	user               *auth.User
	database           database.DB
	creationTime       time.Time
	lastActivityTime   time.Time
	lastHeartBeat      time.Time
	readWriteTxOngoing bool
	transactions       map[string]transactions.Transaction
	log                logger.Logger
}

func NewSession(sessionID string, user *auth.User, db database.DB, log logger.Logger) *Session {
	now := time.Now()
	return &Session{
		id:               sessionID,
		state:            Active,
		user:             user,
		database:         db,
		creationTime:     now,
		lastActivityTime: now,
		lastHeartBeat:    now,
		transactions:     make(map[string]transactions.Transaction),
		log:              log,
	}
}

func (s *Session) NewTransaction(mode schema.TxMode) (transactions.Transaction, error) {
	s.mux.Lock()
	defer s.mux.Unlock()
	sqlTx, _, err := s.database.SQLExec(&schema.SQLExecRequest{Sql: "BEGIN TRANSACTION;"}, nil)
	if err != nil {
		return nil, err
	}
	if mode == schema.TxMode_READ_WRITE {
		if s.readWriteTxOngoing {
			return nil, ErrOngoingReadWriteTx
		}
		s.readWriteTxOngoing = true
	}
	transactionID := xid.New().String()
	tx := transactions.NewTransaction(sqlTx, transactionID, mode, s.database, s.id)
	s.transactions[transactionID] = tx
	return tx, nil
}

func (s *Session) RemoveTransaction(transactionID string) error {
	s.mux.Lock()
	defer s.mux.Unlock()
	return s.removeTransaction(transactionID)
}

// not thread safe
func (s *Session) removeTransaction(transactionID string) error {
	if tx, ok := s.transactions[transactionID]; ok {
		if tx.GetMode() == schema.TxMode_READ_WRITE {
			s.readWriteTxOngoing = false
		}
		delete(s.transactions, transactionID)
		return nil
	}
	return ErrTransactionNotFound
}

func (s *Session) RollbackTransactions() error {
	s.mux.Lock()
	defer s.mux.Unlock()
	merr := multierr.NewMultiErr()
	for _, tx := range s.transactions {
		s.log.Debugf("Deleting transaction %s", tx.GetID())
		if err := tx.Rollback(); err != nil {
			s.log.Errorf("Error while rolling back transaction %s: %v", tx.GetID(), err)
			merr.Append(err)
			continue
		}
		if err := s.removeTransaction(tx.GetID()); err != nil {
			s.log.Errorf("Error while removing transaction %s: %v", tx.GetID(), err)
			merr.Append(err)
			continue
		}
	}
	if merr.HasErrors() {
		return merr
	}
	return nil
}

func (s *Session) GetID() string {
	s.mux.Lock()
	defer s.mux.Unlock()
	return s.id
}

func (s *Session) GetTransaction(transactionID string) (transactions.Transaction, error) {
	s.mux.RLock()
	defer s.mux.RUnlock()
	tx, ok := s.transactions[transactionID]
	if !ok {
		return nil, transactions.ErrTransactionNotFound
	}
	return tx, nil
}

func GetSessionIDFromContext(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", ErrNoSessionAuthDataProvided
	}
	authHeader, ok := md["sessionid"]
	if !ok || len(authHeader) < 1 {
		return "", ErrNoSessionAuthDataProvided
	}
	sessionID := authHeader[0]
	if sessionID == "" {
		return "", ErrNoSessionIDPresent
	}
	return sessionID, nil
}

func GetTransactionIDFromContext(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", ErrNoTransactionAuthDataProvided
	}
	authHeader, ok := md["transactionid"]
	if !ok || len(authHeader) < 1 {
		return "", ErrNoTransactionAuthDataProvided
	}
	transactionID := authHeader[0]
	if transactionID == "" {
		return "", ErrNoTransactionIDPresent
	}
	return transactionID, nil
}

func (s *Session) GetUser() *auth.User {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.user
}

func (s *Session) GetDatabase() database.DB {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.database
}

func (s *Session) SetDatabase(db database.DB) {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.database = db
}

func (s *Session) setStatus(st Status) {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.state = st
}

func (s *Session) GetStatus() Status {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.state
}

func (s *Session) GetLastActivityTime() time.Time {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.lastActivityTime
}

func (s *Session) SetLastActivityTime(t time.Time) {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.lastActivityTime = t
}

func (s *Session) GetLastHeartBeat() time.Time {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.lastHeartBeat
}

func (s *Session) SetLastHeartBeat(t time.Time) {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.lastHeartBeat = t
}

func (s *Session) GetCreationTime() time.Time {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.creationTime
}

func (s *Session) GetReadWriteTxOngoing() bool {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.readWriteTxOngoing
}