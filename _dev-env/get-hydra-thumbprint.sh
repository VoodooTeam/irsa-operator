openssl s_client -connect localhost:4444 < /dev/null 2>/dev/null | openssl x509 -fingerprint -noout -in /dev/stdin | sed 's/.*=\|://g' 
