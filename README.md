# Volatility-Driven Options Trading & Risk Engine

A backend system that behaves like a “trader’s brain” for options—focused on volatility mispricing, risk survival, and scenario-based evaluation.

## Architecture

Modular monolith in Go.
- **cmd/volengine**: Entry point.
- **internal/domain**: Pure domain models.
- **internal/engines**: Core compute logic (Pricing, IV, Vol, Risk).
- **internal/service**: Orchestration.
- **internal/store**: Persistence.

## Usage

### Prerequisites
- Go 1.21+

### Running the Prototype

```bash
go run cmd/volengine/main.go snapshot-run
```
