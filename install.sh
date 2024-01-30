#!/bin/bash

#colors
red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
blue='\033[0;34m'
purple='\033[0;35m'
cyan='\033[0;36m'
rest='\033[0m'
myip=$(hostname -I | awk '{print $1}')

# Function to detect Linux distribution
detect_distribution() {
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
    local packages=("nginx" "git" "jq" "certbot" "python3-certbot-nginx" "wget" "tar")
    
    for package in "${packages[@]}"; do
        if ! dpkg -s "$package" &> /dev/null; then
            echo -e "${yellow}$package is not installed. Installing...${rest}"
            $pm install -y "$package"
        else
            echo -e "${green}$package is already installed.${rest}"
        fi
    done
    
    if ! command -v go &> /dev/null; then
        install_go
    else
        echo -e "${green}go is already installed.${rest}"
    fi
}

# Install Go
install_go() {
    echo -e "${yellow}go is not installed. Installing...${rest}"
    
    ARCH=$(dpkg --print-architecture)
    
    if [[ $ARCH == "amd64" || $ARCH == "arm64" ]]; then
        wget https://go.dev/dl/go1.21.6.linux-"$ARCH".tar.gz
        rm -rf /usr/local/go && rm -rf /usr/local/bin/go && tar -C /usr/local -xzf go1.21.6.linux-"$ARCH".tar.gz
        export PATH=$PATH:/usr/local/go/bin
        cp /usr/local/go/bin/go /usr/local/bin
        
        rm go1.21.6.linux-"$ARCH".tar.gz
        rm -rf go
        echo -e "${cyan}Go has been installed.${rest}"
    else
        echo -e "${red}Unsupported architecture: $ARCH${rest}"
        exit 1
    fi
}

# install SNI service
install() {
    if systemctl is-active --quiet sni.service; then
        echo -e "${green}Service is already installed and active.${rest}"
    else
        install_dependencies
        git clone https://github.com/bepass-org/smartSNI.git /root/smartSNI
         
        sleep 1
        clear
        echo -e "${yellow}********************${rest}"
        read -p "Enter your domain: " domain
        echo -e "${yellow}********************${rest}"
        read -p "Enter domain names (separated by commas)[example: intel.com,youtube]: " site_list
        echo -e "${yellow}********************${rest}"
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

        sudo cp /root/smartSNI/nginx.conf "$nginx_conf"
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
ExecStart=/usr/local/go/bin/go run /root/smartSNI/main.go
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
            echo -e "${yellow}____________________________${rest}"
            echo -e "${green}Service Installed Successfully and activated.${rest}"
            echo -e "${yellow}____________________________${rest}"
        else
            echo -e "${yellow}____________________________${rest}"
            echo -e "${red}The SNI service is not active.${rest}"
            echo -e "${yellow}____________________________${rest}"
        fi
    fi
}

# Uninstall function
uninstall() {
    if [ ! -f "/etc/systemd/system/sni.service" ]; then
        echo -e "${yellow}____________________________${rest}"
        echo -e "${red}The service is not installed.${rest}"
        echo -e "${yellow}____________________________${rest}"
        return
    fi
    # Stop and disable the service
    sudo systemctl stop sni.service
    sudo systemctl disable sni.service

    # Remove service file
    sudo rm /etc/systemd/system/sni.service
    rm -rf /root/smartSNI
    rm -rf /root/go
    echo -e "${yellow}________________________________${rest}"
    echo -e "${green}Uninstallation completed successfully.${rest}"
    echo -e "${yellow}________________________________${rest}"
}

# Show Websites
display_sites() {
    config_file="/root/smartSNI/config.json"

    if [ -d "/root/smartSNI" ]; then
        echo -e "${yellow}****${cyan} [Websites] ${yellow}****${rest}"
        # Initialize a counter
        counter=1
        # Loop through the domains and display with numbering
        jq -r '.domains | keys_unsorted | .[]' "$config_file" | while read -r domain; do
            echo "$counter) $domain"
            ((counter++))
        done
        echo ""
        echo -e "${yellow}********************${rest}"
    else
        echo -e "${red}Error: Not installed. Please Install first.${rest}"
    fi
}

# Check service
check() {
    if systemctl is-active --quiet sni.service; then
        echo -e "${cyan}[Service Actived]${rest}"
    else
        echo -e "${yellow}[Service Not Installed]${rest}"
    fi
}

# Add sites
add_sites() {
    config_file="/root/smartSNI/config.json"

    if [ -d "/root/smartSNI" ]; then
        read -p "Enter additional Websites (separated by commas):" additional_sites
        IFS=',' read -ra new_sites <<< "$additional_sites"

        current_domains=$(jq -r '.domains | keys_unsorted | .[]' "$config_file")
        for site in "${new_sites[@]}"; do
            if [[ ! " ${current_domains[@]} " =~ " $site " ]]; then
                jq ".domains += {\"$site\": \"$myip\"}" "$config_file" > temp_config.json
                mv temp_config.json "$config_file"
                echo -e "${green}Domain ${cyan}'$site'${green} added successfully.${rest}"
            else
                echo -e "${yellow}Domain ${cyan}'$site' already exists.${rest}"
            fi
        done

        # Restart the service
        systemctl restart sni.service
        echo -e "${green}SNI service restarted.${rest}"
    else
        echo -e "${red}Error: Not installed. Please Install first.${rest}"
    fi
}

# Remove sites
remove_sites() {
    config_file="/root/smartSNI/config.json"

    if [ -d "/root/smartSNI" ]; then
        # Display available sites
        display_sites
        
        read -p "Enter Websites names to remove (separated by commas): " domains_to_remove
        IFS=',' read -ra selected_domains <<< "$domains_to_remove"

        # Remove selected domains from JSON
        for selected_domain in "${selected_domains[@]}"; do
            if jq -e --arg selected_domain "$selected_domain" '.domains | has($selected_domain)' "$config_file" > /dev/null; then
                jq "del(.domains[\"$selected_domain\"])" "$config_file" > temp_config.json
                mv temp_config.json "$config_file"
                echo -e "${green}Domain ${cyan}'$selected_domain'${green} removed successfully.${rest}"
            else
                echo -e "${yellow}Domain ${cyan}'$selected_domain'${yellow} not found.${rest}"
            fi
        done

        # Restart the service
        systemctl restart sni.service
        echo -e "${green}SNI service restarted.${rest}"
    else
        echo -e "${red}Error: Not installed. Please Install first.${rest}"
    fi
}

clear
echo -e "${cyan}By --> Peyman * Github.com/Ptechgithub * ${rest}"
check
echo -e "${purple}*******************${rest}"
echo -e "${purple}* ${green}SMART SNI PROXY${purple} *${rest}"
echo -e "${purple}*******************${rest}"
echo -e "${yellow}1) ${green}Install${rest}        ${purple}*"
echo -e "${purple}                  * "
echo -e "${yellow}2) ${green}Uninstall${rest}      ${purple}*"
echo -e "${purple}                  * "
echo -e "${yellow}3) ${green}Show Websites ${rest} ${purple}*"
echo -e "${purple}                  * "
echo -e "${yellow}4) ${green}Add Sites${rest}      ${purple}*"
echo -e "${purple}                  * "
echo -e "${yellow}5) ${green}Remove Sites${rest}   ${purple}*"
echo -e "${purple}                  * "
echo -e "${yellow}0) ${purple}Exit${rest}${purple}           *"
echo -e "${purple}*******************${rest}"
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
    4) 
        add_sites
        ;;
    5)
        remove_sites
        ;;
    0)
        echo -e "${cyan}By 🖐${rest}"
        exit
        ;;
    *)
        echo "Invalid choice. Please select a valid option."
        ;;
esac