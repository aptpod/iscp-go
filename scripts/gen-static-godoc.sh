#!/usr/bin/env bash

set -o nounset
set -o errtrace
set -o pipefail
IFS=$'\n\t'

#===  FUNCTION  ================================================================
#         NAME: gen_html
#  DESCRIPTION: description
#===============================================================================
function gen_html() {
  local __godoc_pid=$(ps | grep godoc | awk '{print $1}')
  if [ "${__godoc_pid}" != "" ]; then
    kill ${__godoc_pid}
  fi
  go install golang.org/x/tools/cmd/godoc@latest
  godoc -http=:6060 >/dev/null &
  __godoc_pid=$!
  cd build/doc/godoc || exit
  # wait for godoc server
  timeout 300 bash -c 'while [[ "$(curl -s -o /dev/null -w ''%{http_code}'' localhost:6060/pkg/github.com/aptpod/iscp-go/)" != "200" ]]; do sleep 1; done' || false
  wget -r -np -N -E -p -k -e robots=off --reject-regex=.*localhost:8080.* http://localhost:6060/pkg/github.com/aptpod/iscp-go/
  kill ${__godoc_pid}
}

#===  FUNCTION  ================================================================
#         NAME: gen_index_html
#  DESCRIPTION: description
#===============================================================================
function gen_index_html() {
  links=$(
    git tag -l |
      sed -E 's/v[0-9]*\.[0-9]*\.[0-9]*1582/&-zzzzz/' |
      sort -V |
      sed -E 's/-zzzzz//g' |
      grep -v "-" |
      sed -e '1,20d' |
      awk '{print "<li><a href=\"./" $1 "/godoc/pkg/github.com/aptpod/iscp-go/index.html\" target=\"_blank\">go-doc-" $1 "</a></li>"}'
  )
  template=$(cat ./scripts/godoc-index.html.tmpl)
  eval "echo \"${template}\"" >./build/doc/godoc/index.html
}

function main() {
  gen_index_html "$@"
  gen_html "$@"
}

main "$@"
