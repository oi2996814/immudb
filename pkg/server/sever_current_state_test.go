/*
Copyright 2022 Codenotary Inc. All rights reserved.

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

package server

import (
	"context"
	"io/ioutil"
	"os"
	"testing"

	"github.com/codenotary/immudb/pkg/api/schema"
	"github.com/codenotary/immudb/pkg/signer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestServerCurrentStateSigned(t *testing.T) {
	dir, err := ioutil.TempDir("", "server_test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	s := DefaultServer()

	s.WithOptions(DefaultOptions().WithDir(dir))

	dbRootpath := dir

	sig, err := signer.NewSigner("./../../test/signer/ec3.key")
	assert.NoError(t, err)

	stSig := NewStateSigner(sig)
	s = s.WithOptions(s.Options.WithAuth(false).WithSigningKey("foo")).WithStateSigner(stSig).(*ImmuServer)

	err = s.loadSystemDatabase(dbRootpath, nil, s.Options.AdminPassword)
	require.NoError(t, err)

	err = s.loadDefaultDatabase(dbRootpath, nil)
	require.NoError(t, err)

	ctx := context.Background()

	_, _ = s.Set(ctx, &schema.SetRequest{
		KVs: []*schema.KeyValue{
			{
				Key:   []byte("Alberto"),
				Value: []byte("Tomba"),
			},
		},
	},
	)

	state, err := s.CurrentState(ctx, &emptypb.Empty{})

	assert.NoError(t, err)
	assert.IsType(t, &schema.ImmutableState{}, state)
	assert.IsType(t, &schema.Signature{}, state.Signature)
	assert.NotNil(t, state.Signature.Signature)
	assert.NotNil(t, state.Signature.PublicKey)

	ecdsaPK, err := signer.UnmarshalKey(state.Signature.PublicKey)
	require.NoError(t, err)

	ok, err := signer.Verify(state.ToBytes(), state.Signature.Signature, ecdsaPK)
	assert.NoError(t, err)
	assert.True(t, ok)
}
