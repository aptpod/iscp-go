package transport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestPipe(t *testing.T) {
	defer goleak.VerifyNone(t)
	t.Run("single", func(t *testing.T) {
		cli, srv := Pipe()
		defer srv.Close()
		defer cli.Close()
		msg := []byte{1, 2, 3, 4, 5}
		go func() {
			require.NoError(t, cli.Write(msg))
		}()
		got, err := srv.Read()
		require.NoError(t, err)
		assert.Equal(t, msg, got)
		assert.Equal(t, cli.TxBytesCounterValue(), srv.RxBytesCounterValue())
		assert.Equal(t, uint64(5), srv.RxBytesCounterValue())
	})

	t.Run("multiple", func(t *testing.T) {
		cli, srv := Pipe()
		msg1 := []byte{1, 2, 3, 4, 5}
		msg2 := []byte{2, 2, 3, 4, 5}
		go func() {
			require.NoError(t, cli.Write(msg1))
			require.NoError(t, cli.Write(msg2))
		}()
		got1, err := srv.Read()
		require.NoError(t, err)
		assert.Equal(t, msg1, got1)
		got2, err := srv.Read()
		require.NoError(t, err)
		assert.Equal(t, msg2, got2)
		assert.Equal(t, cli.TxBytesCounterValue(), srv.RxBytesCounterValue())
		assert.Equal(t, uint64(10), srv.RxBytesCounterValue())
	})

	t.Run("too many", func(t *testing.T) {
		cli, srv := Pipe()
		msg1 := []byte{1, 2, 3, 4, 5}
		go func() {
			for i := 0; i < 100000; i++ {
				require.NoError(t, cli.Write(msg1))
			}
		}()
		for i := 0; i < 100000; i++ {
			got1, err := srv.Read()
			require.NoError(t, err)
			assert.Equal(t, msg1, got1)
		}
		assert.Equal(t, cli.TxBytesCounterValue(), srv.RxBytesCounterValue())
		assert.Equal(t, uint64(100000*5), srv.RxBytesCounterValue())
	})

	t.Run("call read after the local pipe was closed", func(t *testing.T) {
		cli, srv := Pipe()
		require.NoError(t, cli.Close())
		assert.ErrorIs(t, cli.Write([]byte{1, 2, 3, 4, 5}), ErrAlreadyClosed)
		assert.ErrorIs(t, srv.Write([]byte{1, 2, 3, 4, 5}), ErrAlreadyClosed)
	})
}

func TestCopy(t *testing.T) {
	defer goleak.VerifyNone(t)
	t.Run("single", func(t *testing.T) {
		in1, out1 := Pipe()
		defer in1.Close()
		in2, out2 := Pipe()
		defer in2.Close()
		msg := []byte{1, 2, 3, 4, 5}
		msg2 := []byte{2, 2, 3, 4, 5}

		go func() {
			Copy(in2, out1)
		}()

		go func() {
			// reverse
			Copy(out1, in2)
		}()

		go func() {
			require.NoError(t, in1.Write(msg))
			require.NoError(t, out2.Write(msg2))
		}()

		got1, err := out2.Read()
		require.NoError(t, err)
		assert.Equal(t, msg, got1)

		got2, err := in1.Read()
		require.NoError(t, err)
		assert.Equal(t, msg2, got2)
	})

	t.Run("multiple", func(t *testing.T) {
		in1, out1 := Pipe()
		defer in1.Close()
		in2, out2 := Pipe()
		defer in2.Close()
		msg := []byte{1, 2, 3, 4, 5}
		msg2 := []byte{2, 2, 3, 4, 5}

		go func() {
			Copy(in2, out1)
		}()

		go func() {
			// reverse
			Copy(out1, in2)
		}()

		go func() {
			require.NoError(t, in1.Write(msg))
			require.NoError(t, in1.Write(msg))
			require.NoError(t, out2.Write(msg2))
			require.NoError(t, out2.Write(msg2))
		}()

		got1, err := out2.Read()
		require.NoError(t, err)
		assert.Equal(t, msg, got1)

		got1, err = out2.Read()
		require.NoError(t, err)
		assert.Equal(t, msg, got1)

		got2, err := in1.Read()
		require.NoError(t, err)
		assert.Equal(t, msg2, got2)

		got2, err = in1.Read()
		require.NoError(t, err)
		assert.Equal(t, msg2, got2)
	})

	t.Run("error", func(t *testing.T) {
		in1, out1 := Pipe()
		in2, out2 := Pipe()

		errCh := make(chan error, 1)
		go func() {
			defer in2.Close()
			errCh <- Copy(in2, out1)
		}()
		require.NoError(t, in1.Close())
		assert.NoError(t, <-errCh)
		assert.ErrorIs(t, out2.Write([]byte{1, 2, 3, 4, 5}), ErrAlreadyClosed)
		assert.ErrorIs(t, in2.Write([]byte{1, 2, 3, 4, 5}), ErrAlreadyClosed)
		_, err := out2.Read()
		assert.ErrorIs(t, err, EOF)
	})
}
