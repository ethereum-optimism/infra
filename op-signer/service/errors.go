package service

type InvalidTransactionError struct{ message string }

func (e *InvalidTransactionError) Error() string  { return e.message }
func (e *InvalidTransactionError) ErrorCode() int { return -32010 }

type UnauthorizedTransactionError struct{ message string }

func (e *UnauthorizedTransactionError) Error() string  { return e.message }
func (e *UnauthorizedTransactionError) ErrorCode() int { return -32011 }
