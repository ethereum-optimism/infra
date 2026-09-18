package service

type InvalidTransactionError struct{ message string }

func (e *InvalidTransactionError) Error() string  { return e.message }
func (e *InvalidTransactionError) ErrorCode() int { return -32010 }

type UnauthorizedTransactionError struct{ message string }

func (e *UnauthorizedTransactionError) Error() string  { return e.message }
func (e *UnauthorizedTransactionError) ErrorCode() int { return -32011 }

type InvalidBlockPayloadError struct{ message string }

func (e *InvalidBlockPayloadError) Error() string  { return e.message }
func (e *InvalidBlockPayloadError) ErrorCode() int { return -32012 }

type UnauthorizedBlockPayloadError struct{ message string }

func (e *UnauthorizedBlockPayloadError) Error() string  { return e.message }
func (e *UnauthorizedBlockPayloadError) ErrorCode() int { return -32013 }

type InvalidMessageError struct{ message string }

func (e *InvalidMessageError) Error() string  { return e.message }
func (e *InvalidMessageError) ErrorCode() int { return -32014 }

type UnauthorizedMessageError struct{ message string }

func (e *UnauthorizedMessageError) Error() string  { return e.message }
func (e *UnauthorizedMessageError) ErrorCode() int { return 403 }
