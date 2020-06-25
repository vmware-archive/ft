// Code generated by counterfeiter. DO NOT EDIT.
package accountsfakes

import (
	"sync"

	"github.com/concourse/workloads/accounts"
)

type FakeAccountant struct {
	AccountStub        func([]accounts.Container) ([]accounts.Sample, error)
	accountMutex       sync.RWMutex
	accountArgsForCall []struct {
		arg1 []accounts.Container
	}
	accountReturns struct {
		result1 []accounts.Sample
		result2 error
	}
	accountReturnsOnCall map[int]struct {
		result1 []accounts.Sample
		result2 error
	}
	invocations      map[string][][]interface{}
	invocationsMutex sync.RWMutex
}

func (fake *FakeAccountant) Account(arg1 []accounts.Container) ([]accounts.Sample, error) {
	var arg1Copy []accounts.Container
	if arg1 != nil {
		arg1Copy = make([]accounts.Container, len(arg1))
		copy(arg1Copy, arg1)
	}
	fake.accountMutex.Lock()
	ret, specificReturn := fake.accountReturnsOnCall[len(fake.accountArgsForCall)]
	fake.accountArgsForCall = append(fake.accountArgsForCall, struct {
		arg1 []accounts.Container
	}{arg1Copy})
	fake.recordInvocation("Account", []interface{}{arg1Copy})
	fake.accountMutex.Unlock()
	if fake.AccountStub != nil {
		return fake.AccountStub(arg1)
	}
	if specificReturn {
		return ret.result1, ret.result2
	}
	fakeReturns := fake.accountReturns
	return fakeReturns.result1, fakeReturns.result2
}

func (fake *FakeAccountant) AccountCallCount() int {
	fake.accountMutex.RLock()
	defer fake.accountMutex.RUnlock()
	return len(fake.accountArgsForCall)
}

func (fake *FakeAccountant) AccountCalls(stub func([]accounts.Container) ([]accounts.Sample, error)) {
	fake.accountMutex.Lock()
	defer fake.accountMutex.Unlock()
	fake.AccountStub = stub
}

func (fake *FakeAccountant) AccountArgsForCall(i int) []accounts.Container {
	fake.accountMutex.RLock()
	defer fake.accountMutex.RUnlock()
	argsForCall := fake.accountArgsForCall[i]
	return argsForCall.arg1
}

func (fake *FakeAccountant) AccountReturns(result1 []accounts.Sample, result2 error) {
	fake.accountMutex.Lock()
	defer fake.accountMutex.Unlock()
	fake.AccountStub = nil
	fake.accountReturns = struct {
		result1 []accounts.Sample
		result2 error
	}{result1, result2}
}

func (fake *FakeAccountant) AccountReturnsOnCall(i int, result1 []accounts.Sample, result2 error) {
	fake.accountMutex.Lock()
	defer fake.accountMutex.Unlock()
	fake.AccountStub = nil
	if fake.accountReturnsOnCall == nil {
		fake.accountReturnsOnCall = make(map[int]struct {
			result1 []accounts.Sample
			result2 error
		})
	}
	fake.accountReturnsOnCall[i] = struct {
		result1 []accounts.Sample
		result2 error
	}{result1, result2}
}

func (fake *FakeAccountant) Invocations() map[string][][]interface{} {
	fake.invocationsMutex.RLock()
	defer fake.invocationsMutex.RUnlock()
	fake.accountMutex.RLock()
	defer fake.accountMutex.RUnlock()
	copiedInvocations := map[string][][]interface{}{}
	for key, value := range fake.invocations {
		copiedInvocations[key] = value
	}
	return copiedInvocations
}

func (fake *FakeAccountant) recordInvocation(key string, args []interface{}) {
	fake.invocationsMutex.Lock()
	defer fake.invocationsMutex.Unlock()
	if fake.invocations == nil {
		fake.invocations = map[string][][]interface{}{}
	}
	if fake.invocations[key] == nil {
		fake.invocations[key] = [][]interface{}{}
	}
	fake.invocations[key] = append(fake.invocations[key], args)
}

var _ accounts.Accountant = new(FakeAccountant)
