# from repo root, run as Administrator
cd tools\net-create
go build -o net-create.exe .

# list what's there now
.\net-create.exe -list

# create the nat network
.\net-create.exe -name nat -type NAT -subnet 172.20.0.0/16 -gateway 172.20.0.1

# verify
.\net-create.exe -list

# then use it
..\..\container-cli.exe run --base <path> --scratch <path> --network nat -- cmd.exe