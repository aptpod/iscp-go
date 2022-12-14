// Code generated by MockGen. DO NOT EDIT.
// Source: ./transport.go

// Package wiremock is a generated GoMock package.
package wiremock

import (
	reflect "reflect"

	encoding "github.com/aptpod/iscp-go/encoding"
	message "github.com/aptpod/iscp-go/message"
	gomock "github.com/golang/mock/gomock"
)

// MockEncodingTransport is a mock of EncodingTransport interface.
type MockEncodingTransport struct {
	ctrl     *gomock.Controller
	recorder *MockEncodingTransportMockRecorder
}

// MockEncodingTransportMockRecorder is the mock recorder for MockEncodingTransport.
type MockEncodingTransportMockRecorder struct {
	mock *MockEncodingTransport
}

// NewMockEncodingTransport creates a new mock instance.
func NewMockEncodingTransport(ctrl *gomock.Controller) *MockEncodingTransport {
	mock := &MockEncodingTransport{ctrl: ctrl}
	mock.recorder = &MockEncodingTransportMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockEncodingTransport) EXPECT() *MockEncodingTransportMockRecorder {
	return m.recorder
}

// Close mocks base method.
func (m *MockEncodingTransport) Close() error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Close")
	ret0, _ := ret[0].(error)
	return ret0
}

// Close indicates an expected call of Close.
func (mr *MockEncodingTransportMockRecorder) Close() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Close", reflect.TypeOf((*MockEncodingTransport)(nil).Close))
}

// Read mocks base method.
func (m *MockEncodingTransport) Read() (message.Message, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Read")
	ret0, _ := ret[0].(message.Message)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Read indicates an expected call of Read.
func (mr *MockEncodingTransportMockRecorder) Read() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Read", reflect.TypeOf((*MockEncodingTransport)(nil).Read))
}

// RxCount mocks base method.
func (m *MockEncodingTransport) RxCount() *encoding.Count {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "RxCount")
	ret0, _ := ret[0].(*encoding.Count)
	return ret0
}

// RxCount indicates an expected call of RxCount.
func (mr *MockEncodingTransportMockRecorder) RxCount() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "RxCount", reflect.TypeOf((*MockEncodingTransport)(nil).RxCount))
}

// RxMessageCounterValue mocks base method.
func (m *MockEncodingTransport) RxMessageCounterValue() uint64 {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "RxMessageCounterValue")
	ret0, _ := ret[0].(uint64)
	return ret0
}

// RxMessageCounterValue indicates an expected call of RxMessageCounterValue.
func (mr *MockEncodingTransportMockRecorder) RxMessageCounterValue() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "RxMessageCounterValue", reflect.TypeOf((*MockEncodingTransport)(nil).RxMessageCounterValue))
}

// TxCount mocks base method.
func (m *MockEncodingTransport) TxCount() *encoding.Count {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "TxCount")
	ret0, _ := ret[0].(*encoding.Count)
	return ret0
}

// TxCount indicates an expected call of TxCount.
func (mr *MockEncodingTransportMockRecorder) TxCount() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "TxCount", reflect.TypeOf((*MockEncodingTransport)(nil).TxCount))
}

// TxMessageCounterValue mocks base method.
func (m *MockEncodingTransport) TxMessageCounterValue() uint64 {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "TxMessageCounterValue")
	ret0, _ := ret[0].(uint64)
	return ret0
}

// TxMessageCounterValue indicates an expected call of TxMessageCounterValue.
func (mr *MockEncodingTransportMockRecorder) TxMessageCounterValue() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "TxMessageCounterValue", reflect.TypeOf((*MockEncodingTransport)(nil).TxMessageCounterValue))
}

// Write mocks base method.
func (m *MockEncodingTransport) Write(message message.Message) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Write", message)
	ret0, _ := ret[0].(error)
	return ret0
}

// Write indicates an expected call of Write.
func (mr *MockEncodingTransportMockRecorder) Write(message interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Write", reflect.TypeOf((*MockEncodingTransport)(nil).Write), message)
}
