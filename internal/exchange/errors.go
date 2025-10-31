package exchange

import (
	"errors"

	ccxt "github.com/ccxt/ccxt/go/v4"
)

var (
	// ErrMaintenance 表示交易所处于维护状态，需要上层跳过交易。
	ErrMaintenance = errors.New("exchange on maintenance")
)

// IsRetryable 判断错误是否可重试。
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	var ccxtErr *ccxt.Error
	if errors.As(err, &ccxtErr) {
		switch ccxtErr.Type {
		case ccxt.NetworkErrorErrType,
			ccxt.RequestTimeoutErrType,
			ccxt.ExchangeNotAvailableErrType,
			ccxt.RateLimitExceededErrType,
			ccxt.DDoSProtectionErrType,
			ccxt.BadResponseErrType,
			ccxt.NullResponseErrType:
			return true
		default:
			return false
		}
	}

	return false
}
