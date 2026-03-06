package obs

import "expvar"

var (
	IngestionTotal = expvar.NewInt("ingestion_total")
	ForecastPulls  = expvar.NewInt("forecast_pulls")
	AuditErrors    = expvar.NewInt("audit_errors")
)
