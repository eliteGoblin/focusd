#!/bin/zsh

file_path=""
focusdPath=$(dirname "$0")

[[ "$1" == "w" ]] && {file_path=${focusdPath}/../data/white.csv;}
[[ "$1" == "b" ]] && {file_path=${focusdPath}/../data/black.csv;}
domain=""
[[ -z "$2" ]] && { cat ${file_path};  return 0; }
[[ "$2" == "open" ]] && {xdg-open ${file_path}; return 0;}
[[ "$2" == "host" ]] && {while read line; do echo "localhost "$line; done < ${file_path}; return 0;}
domain=$(echo $2 | sed -e "s/[^/]*\/\/\([^@]*@\)\?\([^:/]*\).*/\2/")
[[ "$(grep -c "^$domain" ${file_path})" -ge 1 ]] && {echo "already exist"; return 1;}
echo "$domain" >>${file_path}
sort -u -o ${file_path} ${file_path}
cat ${file_path}