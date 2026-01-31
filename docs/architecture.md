# Architecture & Design Rules

## Core Philosophy
- **Modular Monolith**: Single Go application with clean internal boundaries.
- **Independence**: Engines and Domain logic must be independent of infrastructure.
- **Reproducibility**: "Why did it recommend this?" must be answerable from stored data.

## Dependency Rules (Strict)

1.  **Transport -> Service -> Engines -> Domain**
    - `transport` (HTTP/CLI) calls `service`.
    - `service` orchestrates `engines` and `store`.
    - `engines` operate on `domain` objects.
    - `domain` is pure data/logic, depends on nothing.

2.  **Store -> Service**
    - `store` provides data access to `service`.
    - `store` and `transport` NEVER depend on each other.

3.  **Engines & Domain are PURE**
    - `engines` and `domain` packages MUST NOT import `store`, `transport`, `config`, or external APIs.
    - They accept inputs and return outputs.
    - No DB calls inside engines.

## Walking Skeleton Flow
`RunSnapshot(snapshot_file) -> RecommendationReport`

1.  **Ingest**: Load and normalize snapshot.
2.  **Build Chain**: Construct `OptionChain` object (ATM, TTE, Moneyness).
3.  **Compute**: Calculate Greeks & IV.
4.  **Regime**: Detect Volatility Regime.
5.  **Strategy**: Propose candidates.
6.  **Simulate**: Run scenario grid.
7.  **Risk**: validate against limits.
8.  **Report**: Generate and persist artifacts.
