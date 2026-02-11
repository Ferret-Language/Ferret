fun printArr(data: []i32, n: i32) {
    for v in data {
        if v == n {
            continue;
        } 
        io::Println(v);
    }
}