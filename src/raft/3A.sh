#!/bin/bash

# 定义变量存储统计结果
non_zero_count=0

# 循环运行测试命令1000次
for i in {1..1000}; do
    # 运行测试命令并记录exit status
    go test -run 3A -race > result.txt
    exit_status=$?

    # 打印exit status
    echo "Exit status of iteration $i: $exit_status"

    # 如果exit status非零，则增加计数器
    if [ $exit_status -eq 1 ]; then
        break
    fi
done

# 输出统计结果
echo "Total non-zero exit status count: $non_zero_count"
