module github.com/TomKodesh/keybridge

go 1.17

require (
	github.com/Microsoft/go-winio v0.4.16
	github.com/bi-zone/wmi v1.1.4
	github.com/fullsailor/pkcs7 v0.0.0-20190404230743-d7302db945fa
	github.com/hattya/go.notify v0.0.0-20200507123844-18670158b53e
	github.com/linuxkit/virtsock v0.0.0-20180830132707-8e79449dea07
	golang.org/x/crypto v0.0.0-20200510223506-06a226fb4e37
	golang.org/x/sys v0.0.0-20210223212115-eede4237b368
)

require (
	github.com/bi-zone/go-ole v1.2.5 // indirect
	github.com/go-ole/go-ole v1.2.4 // indirect
	github.com/hashicorp/errwrap v1.0.0 // indirect
	github.com/hashicorp/go-multierror v1.0.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/scjalliance/comshim v0.0.0-20190308082608-cf06d2532c4e // indirect
)

replace github.com/hattya/go.notify v0.0.0-20200507123844-18670158b53e => github.com/buptczq/go.notify v0.0.0-20210108030838-37adc71f67d9

replace github.com/Microsoft/go-winio v0.4.16 => github.com/buptczq/go-winio v0.4.16-1
