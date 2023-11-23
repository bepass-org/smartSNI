#!/bin/bash

detect_distribution() {
    # Detect the Linux distribution
    local supported_distributions=("ubuntu" "debian" "centos" "fedora")
    
    if [ -f /etc/os-release ]; then
        source /etc/os-release
        if [[ "${ID}" = "ubuntu" || "${ID}" = "debian" || "${ID}" = "centos" || "${ID}" = "fedora" ]]; then
            pm="apt"
            [ "${ID}" = "centos" ] && pm="yum"
            [ "${ID}" = "fedora" ] && pm="dnf"
        else
            echo "Unsupported distribution!"
            exit 1
        fi
    else
        echo "Unsupported distribution!"
        exit 1
    fi
}

# Install necessary packages
install_dependencies() {
    detect_distribution
    $pm update -y
    local packages=("nginx" "git" "jq" "certbot" "python3-certbot-nginx" "snapd")
    
    for package in "${packages[@]}"; do
        if ! dpkg -s "$package" &> /dev/null; then
            echo "$package is not installed. Installing..."
            $pm install -y "$package"
        else
            echo "$package is already installed."
        fi
    done
    
    if ! snap list go &> /dev/null; then
        echo "go is not installed. Installing..."
        snap install go --classic
    else
        echo "go is already installed."
    fi
}

#install
install() {
    if systemctl is-active --quiet sni.service; then
        echo "The SNI service is already installed and active."
    else
        install_dependencies
        myip=$(hostname -I | awk '{print $1}')
        git clone https://github.com/bepass-org/smartSNI.git /root/smartSNI

        clear
        read -p "Enter your domain: " domain
        read -p "Enter the domain names separated by commas (example: google,youtube): " site_list
        # Split the input into an array
        IFS=',' read -ra sites <<< "$site_list"
        
        # Prepare a string with the new domains
        new_domains="{"
        for ((i = 0; i < ${#sites[@]}; i++)); do
            new_domains+="\"${sites[i]}\": \"$myip\""
            if [ $i -lt $((${#sites[@]}-1)) ]; then
                new_domains+=", "
            fi
        done
        new_domains+="}"
        
        # Create a JSON Object with host and domains
        json_content="{ \"host\": \"$domain\", \"domains\": $new_domains }"
        
        # Save JSON to config.json file
        echo "$json_content" | jq '.' > /root/smartSNI/config.json

        nginx_conf="/etc/nginx/sites-enabled/default"
        sed -i "s/server_name _;/server_name $domain;/g" "$nginx_conf"
        sed -i "s/<YOUR_HOST>/$domain/g" /root/smartSNI/nginx.conf

        # Obtain SSL certificates
        certbot --nginx -d $domain --register-unsafely-without-email --non-interactive --agree-tos --redirect

        # Copy config
        sudo cp /root/smartSNI/nginx.conf "$nginx_conf"

        # Stop and restart nginx
        systemctl stop nginx
        systemctl restart nginx

        config_file="/root/smartSNI/config.json"

        sed -i "s/<YOUR_HOST>/$domain/g" "$config_file"
        sed -i "s/<YOUR_IP>/$myip/g" "$config_file"
        
        # Create systemd service file
        cat > /etc/systemd/system/sni.service <<EOL
[Unit]
Description=Smart SNI Service

[Service]
User=root
WorkingDirectory=/root/smartSNI
ExecStart=/snap/bin/go run main.go
Restart=always

[Install]
WantedBy=default.target
EOL

        # Reload systemd, enable and start the service
        systemctl daemon-reload
        systemctl enable sni.service
        systemctl start sni.service

        # Check if the service is active
        if systemctl is-active --quiet sni.service; then
            echo "The SNI service is now active."
        else
            echo "The SNI service is not active."
        fi
    fi
}

# Uninstall function
uninstall() {
    # Check if the service is installed
    if [ ! -f "/etc/systemd/system/sni.service" ]; then
        echo "The service is not installed."
        return
    fi
    # Stop and disable the service
    sudo systemctl stop sni.service
    sudo systemctl disable sni.service

    # Remove service file
    sudo rm /etc/systemd/system/sni.service
    echo "Uninstallation completed successfully."
}

display_sites() {
    config_file="/root/smartSNI/config.json"

    if [ -d "/root/smartSNI" ]; then
        echo "Current list of sites in $config_file:"
        echo "---------------------"
        jq -r '.domains | keys[]' "$config_file"
        echo "---------------------"
    else
        echo "Error: smartSNI directory not found. Please Install first."
    fi
}

check() {
    if systemctl is-active --quiet sni.service; then
        echo "[Service Is Active]"
    else
        echo "[Service Is Not active]"
    fi
}


clear
echo "By --> Peyman * Github.com/Ptechgithub * "
echo "--*-* SMART SNI PROXY *-*--"
echo ""
echo "Select an option:"
echo "1) Install"
echo "2) Uninstall"
echo "---------------------------"
echo "3) Show Sites"
echo "0) Exit"
echo "----$(check)----"
read -p "Enter your choice: " choice
case "$choice" in
    1)
        install
        ;;
    2)
        uninstall
        ;;
     3) 
        display_sites
        ;;
    0)   
        exit
        ;;
    *)
        echo "Invalid choice. Please select a valid option."
        ;;
esac