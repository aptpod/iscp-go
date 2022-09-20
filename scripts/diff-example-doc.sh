#!/usr/bin/env bash
set -o nounset
set -o errexit
trap 'echo "Aborting due to errexit on line $LINENO. Exit code: $?" >&2' ERR
set -o errtrace
set -o pipefail
IFS=$'\n\t'

#===  FUNCTION  ================================================================
#         NAME: main
#  DESCRIPTION: doc内のソースコードとExampleの比較する
#    ARGUMENTS: $1 doc.goの比較開始行数 e.g.10
#               $2 doc.goの比較終了行数 e.g. 29
#               $3 比較するgoファイル名 e.g. ../examples/hello-world/server/main.go
#===============================================================================
function main() {
  _doc=$(sed -n "$1,$2p" ./doc.go | sed "s;^\t;;g")
  _example=$(cat $3)
  diff <(echo ${_doc}) <(echo ${_example})
}

main $@
