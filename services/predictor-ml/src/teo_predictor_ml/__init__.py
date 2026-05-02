"""TEO ML predictor service.

Implements the same gRPC contract as the Go heuristic predictor (ADR-0019).
Falls back gracefully: the Go Run Manager will route to the heuristic on outage.
"""

__version__ = "0.1.0"
