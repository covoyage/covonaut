
__trash_rm_move_to_trash() {
    local file_path="$1"
    local file_name
    local trash_dir="$HOME/.Trash"

    file_name=$(basename "$file_path")

    if command -v trash >/dev/null 2>&1; then
        trash "$file_path"
        return $?
    fi

    if [ ! -d "$trash_dir" ]; then
        mkdir -p "$trash_dir"
    fi

    local target_path="$trash_dir/$file_name"

    if [ -e "$target_path" ]; then
        local timestamp=$(date +%Y%m%d_%H%M%S)
        local name_part="${file_name%.*}"
        local ext_part="${file_name##*.}"
        if [ "$name_part" = "$ext_part" ] || [ -z "$name_part" ]; then
            target_path="$trash_dir/${file_name}_${timestamp}_$$"
        else
            target_path="$trash_dir/${name_part}_${timestamp}_$$.${ext_part}"
        fi
    fi

    /bin/mv "$file_path" "$target_path"
    return $?
}

rm() {
    local force_mode=false
    local files_to_trash=()

    for arg in "$@"; do
        case "$arg" in
            -f|--force|-rf|-fr|-Rf|-fR|-FR|-rF)
                force_mode=true
                ;;
            -r|-R|--recursive|-i|--interactive|-v|--verbose)
                ;;
            -*)
                ;;
            *)
                files_to_trash+=("$arg")
                ;;
        esac
    done

    for file in "${files_to_trash[@]}"; do
        if [ ! -e "$file" ] && [ ! -L "$file" ]; then
            if [ "$force_mode" = false ]; then
                echo "rm: $file: No such file or directory" >&2
            fi
            continue
        fi

        __trash_rm_move_to_trash "$file"
    done
    return 0
}
