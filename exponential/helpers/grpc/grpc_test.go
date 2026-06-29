package grpc

import (
	"fmt"
	"testing"

	"github.com/Azure/retry/internal/errors"
	"github.com/google/go-cmp/cmp"
	"github.com/kylelemons/godebug/pretty"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestErrTransformer(t *testing.T) {
	t.Parallel()

	tr, err := New(WithExtraCodes(codes.NotFound))
	if err != nil {
		panic(err)
	}
	for i := 1; i < 16; i++ { // 16 is the max Code in gRPC at this time and 0 is OK
		wantPermErr := true
		code := codes.Code(i)
		if grpcRetriable[code] || code == codes.NotFound {
			wantPermErr = false
		}
		err := status.Error(code, "test error")
		got := tr.ErrTransformer(err)

		permErr := errors.Is(got, errors.ErrPermanent)
		if permErr != wantPermErr {
			t.Errorf("TestErrTransformer(%s): wrong error type for code", code)
		}
	}
}

func TestIsGRPCErr(t *testing.T) {
	t.Parallel()

	tr := &Transformer{}

	tests := []struct {
		name string
		err  error
		code codes.Code
		want bool
	}{
		{
			name: "Non-gRPC error",
			err:  fmt.Errorf("not a grpc error"),
			code: codes.Unknown,
			want: false,
		},
		{
			name: "gRPC error with codes.Unknown",
			err:  status.Error(codes.Unknown, "unknown error"),
			code: codes.Unknown,
			want: true,
		},
		{
			name: "gRPC error",
			err:  status.Error(codes.Unavailable, "transient error"),
			code: codes.Unavailable,
			want: true,
		},
		{
			name: "gRPC error, but status code is OK",
			err:  status.Error(codes.OK, "OK"),
			code: codes.OK,
			want: false,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gotOk, code := tr.isGRPCErr(test.err)
			if gotOk != test.want {
				t.Errorf("isGRPCErr(): got %v, want %v", gotOk, test.want)
			}
			if code != test.code {
				t.Errorf("isGRPCErr(): got %v, want %v", code, test.code)
			}
		})
	}
}

func TestIsGRPCPermanent(t *testing.T) {
	t.Parallel()

	tr := &Transformer{}

	for code := range grpcRetriable {
		code := code
		t.Run(code.String(), func(t *testing.T) {
			if got := tr.isGRPCPermanent(code); got {
				t.Errorf("isGRPCPermanent(): got %v, want %v", got, false)
			}
		})
	}
	if got := tr.isGRPCPermanent(codes.PermissionDenied); !got {
		t.Errorf("isGRPCPermanent(%v): got %v, want %v", codes.PermissionDenied, got, true)
	}
}

func TestRespToErr(t *testing.T) {
	t.Parallel()

	someErr := fmt.Errorf("some error")
	respHadErr := fmt.Errorf("response error")
	unexpectedErr := fmt.Errorf("unexpected response")

	tests := []struct {
		name         string
		respArg      proto.Message
		errArg       error
		protosToErrs []ProtoToErr
		wantErr      error
	}{
		{
			name:    "Success: error was nil and no transformers",
			respArg: wrapperspb.String("test"),
			errArg:  nil,
			wantErr: nil,
		},
		{
			name:    "Success: transformer returns nil",
			respArg: wrapperspb.String("test"),
			errArg:  nil,
			protosToErrs: []ProtoToErr{
				func(msg proto.Message) error {
					return nil
				},
			},
			wantErr: nil,
		},
		{
			name:    "Error: incoming error is set, transformers are not run",
			respArg: wrapperspb.String("test"),
			errArg:  someErr,
			protosToErrs: []ProtoToErr{
				func(msg proto.Message) error {
					return unexpectedErr
				},
			},
			wantErr: someErr,
		},
		{
			name:    "Error: single transformer returns an error",
			respArg: wrapperspb.String("test"),
			errArg:  nil,
			protosToErrs: []ProtoToErr{
				func(msg proto.Message) error {
					return respHadErr
				},
			},
			wantErr: respHadErr,
		},
		{
			name:    "Error: two transformers fire and both errors are accumulated",
			respArg: wrapperspb.String("test"),
			errArg:  nil,
			protosToErrs: []ProtoToErr{
				func(msg proto.Message) error {
					return respHadErr
				},
				func(msg proto.Message) error {
					return unexpectedErr
				},
			},
			wantErr: fmt.Errorf("%w: %w", respHadErr, unexpectedErr),
		},
		{
			name:    "Error: first transformer is permanent so the second is not run",
			respArg: wrapperspb.String("test"),
			errArg:  nil,
			protosToErrs: []ProtoToErr{
				func(msg proto.Message) error {
					return fmt.Errorf("%w: %w", someErr, errors.ErrPermanent)
				},
				func(msg proto.Message) error {
					return unexpectedErr
				},
			},
			wantErr: fmt.Errorf("%w: %w", someErr, errors.ErrPermanent),
		},
	}

	for _, test := range tests {
		tr := &Transformer{protosToErrs: test.protosToErrs}
		gotResp, gotErr := tr.RespToErr(test.respArg, test.errArg)
		if diff := cmp.Diff(test.respArg, gotResp, protocmp.Transform()); diff != "" {
			t.Errorf("TestRespToErr(%s): response -want/+got:\n%s", test.name, diff)
			continue
		}
		if diff := pretty.Compare(test.wantErr, gotErr); diff != "" {
			t.Errorf("TestRespToErr(%s): error -want/+got:\n%s", test.name, diff)
		}
	}
}
