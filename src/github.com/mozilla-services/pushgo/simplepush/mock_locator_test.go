// Automatically generated by MockGen. DO NOT EDIT!
// Source: src/github.com/mozilla-services/pushgo/simplepush/locator.go

package simplepush

import (
	gomock "github.com/rafrombrc/gomock/gomock"
)

// Mock of Locator interface
type MockLocator struct {
	ctrl     *gomock.Controller
	recorder *_MockLocatorRecorder
}

// Recorder for MockLocator (not exported)
type _MockLocatorRecorder struct {
	mock *MockLocator
}

func NewMockLocator(ctrl *gomock.Controller) *MockLocator {
	mock := &MockLocator{ctrl: ctrl}
	mock.recorder = &_MockLocatorRecorder{mock}
	return mock
}

func (_m *MockLocator) EXPECT() *_MockLocatorRecorder {
	return _m.recorder
}

func (_m *MockLocator) Close() error {
	ret := _m.ctrl.Call(_m, "Close")
	ret0, _ := ret[0].(error)
	return ret0
}

func (_mr *_MockLocatorRecorder) Close() *gomock.Call {
	return _mr.mock.ctrl.RecordCall(_mr.mock, "Close")
}

func (_m *MockLocator) Contacts(uaid string) ([]string, error) {
	ret := _m.ctrl.Call(_m, "Contacts", uaid)
	ret0, _ := ret[0].([]string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

func (_mr *_MockLocatorRecorder) Contacts(arg0 interface{}) *gomock.Call {
	return _mr.mock.ctrl.RecordCall(_mr.mock, "Contacts", arg0)
}

func (_m *MockLocator) Status() (bool, error) {
	ret := _m.ctrl.Call(_m, "Status")
	ret0, _ := ret[0].(bool)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

func (_mr *_MockLocatorRecorder) Status() *gomock.Call {
	return _mr.mock.ctrl.RecordCall(_mr.mock, "Status")
}
