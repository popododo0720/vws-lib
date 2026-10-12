module example.com/vws-consumer

go 1.26.0

require github.com/popododo0720/vws-lib v0.0.0

// A real consumer vendors a tag into third_party/vws-lib and points replace there.
// This sample points at the checkout it lives in, so CI builds what is about to be merged.
replace github.com/popododo0720/vws-lib => ../..
