package ingest

import (
	"context"

	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"

	// Alloy and the OTel Collector gzip OTLP exports by default; without this
	// decompressor every mirrored payload is rejected as a permanent error.
	_ "google.golang.org/grpc/encoding/gzip"
)

// NewGRPCServer returns a gRPC server exposing the three OTLP export services
// backed by the pipeline. The receiver is read-only and never in the user's
// critical path — it is fed by a second (sampled) exporter in Alloy.
func NewGRPCServer(p *Pipeline) *grpc.Server {
	srv := grpc.NewServer()
	ptraceotlp.RegisterGRPCServer(srv, &traceSvc{p: p})
	pmetricotlp.RegisterGRPCServer(srv, &metricSvc{p: p})
	plogotlp.RegisterGRPCServer(srv, &logSvc{p: p})
	return srv
}

type traceSvc struct {
	ptraceotlp.UnimplementedGRPCServer
	p *Pipeline
}

func (s *traceSvc) Export(_ context.Context, req ptraceotlp.ExportRequest) (ptraceotlp.ExportResponse, error) {
	s.p.ConsumeTraces(req.Traces())
	return ptraceotlp.NewExportResponse(), nil
}

type metricSvc struct {
	pmetricotlp.UnimplementedGRPCServer
	p *Pipeline
}

func (s *metricSvc) Export(_ context.Context, req pmetricotlp.ExportRequest) (pmetricotlp.ExportResponse, error) {
	s.p.ConsumeMetrics(req.Metrics())
	return pmetricotlp.NewExportResponse(), nil
}

type logSvc struct {
	plogotlp.UnimplementedGRPCServer
	p *Pipeline
}

func (s *logSvc) Export(_ context.Context, req plogotlp.ExportRequest) (plogotlp.ExportResponse, error) {
	s.p.ConsumeLogs(req.Logs())
	return plogotlp.NewExportResponse(), nil
}
