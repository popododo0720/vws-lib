// Package errs holds the error codes and kinds shared by VWS services and their mapping to
// gRPC and AWS error codes.
//
// The Code and Kind sets are console-api's resource.Code and resource.Kind, so existing
// mappings carry over. E is the error every layer returns: Message is for the caller, the
// cause is for logs. GRPCStatus never sends the cause or an internal message to a client.
// Reported marks an error that obs/report already recorded, so an outer layer does not
// report it twice. Design: https://github.com/popododo0720/vws-deploy/issues/42.
package errs
